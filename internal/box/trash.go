package box

// Trash for the file explorer: a delete moves the item into <root>/.nxbox-trash/<id>/<name>
// (same root, so it is a rename and keeps owners and modes) with <id>.json holding where it came
// from. Items can be restored or deleted for good; the web process purges items older than the
// configured number of days once a day. Paths inside .nxbox-trash are not visible to browsing,
// search or any other file feature (visibleIn), only to these actions.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const trashDirName = ".nxbox-trash"

var trashIDRe = regexp.MustCompile(`^[0-9]{10}-[0-9a-f]{12}$`)

type trashItem struct {
	ID      string `json:"id"`
	Root    string `json:"root"`    // the root as configured (what the UI shows)
	Path    string `json:"path"`    // where it was, as the user saw it
	Name    string `json:"name"`    // base name
	Dir     bool   `json:"dir"`     // a folder
	Deleted int64  `json:"deleted"` // unix time
	Size    int64  `json:"size"`
}

func inTrash(p string) bool {
	return strings.Contains(p+"/", "/"+trashDirName+"/")
}

// The trash is root's own: <root>/.nxbox-trash and each <id> folder must be real directories
// (never symlinks) owned by the helper's user with mode 0700. They are opened with O_NOFOLLOW
// and every operation inside runs relative to those descriptors (openat, renameat, unlinkat),
// so a user who can write to the root folder cannot swap a symlink in between. The metadata
// JSON is untrusted: names come from the directory entry, ids must match trashIDRe.

const dirFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC

var errTrashUnsafe = userError{"The trash folder of this place is not Box's own (wrong owner, mode or a link). Box does not use it."}

func checkOwnDir(fd int) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR || int(st.Uid) != os.Geteuid() || st.Mode&0o777 != 0o700 {
		return errTrashUnsafe
	}
	return nil
}

// openTrash opens (and with create, makes) the trash of root r. A missing trash without create
// returns -1 and no error.
func openTrash(r rootInfo, create bool) (int, error) {
	rootFD, err := unix.Open(r.real, dirFlags, 0)
	if err != nil {
		return -1, err
	}
	defer unix.Close(rootFD)
	if create {
		if err := unix.Mkdirat(rootFD, trashDirName, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
			return -1, err
		}
	}
	fd, err := unix.Openat(rootFD, trashDirName, dirFlags, 0)
	if err != nil {
		if errors.Is(err, unix.ENOENT) && !create {
			return -1, nil
		}
		if errors.Is(err, unix.ELOOP) || errors.Is(err, unix.ENOTDIR) {
			return -1, errTrashUnsafe
		}
		return -1, err
	}
	if err := checkOwnDir(fd); err != nil {
		unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

func openItemDir(trashFD int, id string) (int, error) {
	if !trashIDRe.MatchString(id) {
		return -1, uerr("This item is not in the trash.")
	}
	fd, err := unix.Openat(trashFD, id, dirFlags, 0)
	if err != nil {
		return -1, errTrashUnsafe
	}
	if err := checkOwnDir(fd); err != nil {
		unix.Close(fd)
		return -1, err
	}
	return fd, nil
}

// itemName is the single entry inside an item folder: a valid base name, not a symlink.
func itemName(itemFD int) (string, bool, error) {
	names, err := readDirNamesFD(itemFD) // the caller owns itemFD
	if err != nil || len(names) != 1 {
		return "", false, uerr("This trash item is damaged.")
	}
	name := names[0]
	if name != filepath.Base(name) || checkName(name) != nil {
		return "", false, uerr("This trash item is damaged.")
	}
	var st unix.Stat_t
	if err := unix.Fstatat(itemFD, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil || st.Mode&unix.S_IFMT == unix.S_IFLNK {
		return "", false, uerr("This trash item is damaged.")
	}
	return name, st.Mode&unix.S_IFMT == unix.S_IFDIR, nil
}

// readDirNamesFD lists a directory through a duplicate of its descriptor (the original stays open
// and is never wrapped in an *os.File, whose finalizer would close it).
func readDirNamesFD(fd int) ([]string, error) {
	dup, err := unix.Dup(fd)
	if err != nil {
		return nil, err
	}
	d := os.NewFile(uintptr(dup), "dir")
	defer d.Close()
	return d.Readdirnames(-1)
}

// removeAllAt deletes name inside dirFD and everything below it without following symlinks.
func removeAllAt(dirFD int, name string) error {
	var st unix.Stat_t
	if err := unix.Fstatat(dirFD, name, &st, unix.AT_SYMLINK_NOFOLLOW); err != nil {
		if errors.Is(err, unix.ENOENT) {
			return nil
		}
		return err
	}
	if st.Mode&unix.S_IFMT != unix.S_IFDIR {
		return unix.Unlinkat(dirFD, name, 0)
	}
	fd, err := unix.Openat(dirFD, name, dirFlags, 0)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), name)
	names, err := f.Readdirnames(-1)
	if err == nil {
		for _, n := range names {
			if err = removeAllAt(fd, n); err != nil {
				break
			}
		}
	}
	f.Close()
	if err != nil {
		return err
	}
	return unix.Unlinkat(dirFD, name, unix.AT_REMOVEDIR)
}

var errCrossDevice = userError{"needs_permanent: This item is on another drive than its folder's trash, so it can only be deleted for good."}

// moveToTrash moves target (inside root r) into r's trash. Symlinks are not trashed (deleted for
// good instead), so the trash never holds a link.
func moveToTrash(r rootInfo, target, shown string) (trashItem, error) {
	info, err := os.Lstat(target)
	if err != nil {
		return trashItem{}, uerr("This file or folder does not exist.")
	}
	if info.Mode()&fs.ModeSymlink != 0 {
		return trashItem{}, userError{"needs_permanent: A link is not moved to the trash. Delete the link for good (what it points to stays)."}
	}
	b := make([]byte, 6)
	rand.Read(b)
	it := trashItem{ID: strconv.FormatInt(time.Now().Unix(), 10) + "-" + hex.EncodeToString(b), Root: r.path, Path: shown,
		Name: filepath.Base(target), Dir: info.IsDir(), Deleted: time.Now().Unix()}
	if info.Mode().IsRegular() {
		it.Size = info.Size()
	} else if info.IsDir() {
		it.Size = treeSize(target)
	}
	tfd, err := openTrash(r, true)
	if err != nil {
		return it, friendlyTrash(err)
	}
	defer unix.Close(tfd)
	if err := unix.Mkdirat(tfd, it.ID, 0o700); err != nil {
		return it, err
	}
	ifd, err := openItemDir(tfd, it.ID)
	if err != nil {
		return it, err
	}
	defer unix.Close(ifd)
	pfd, base, err := openBeneath(r.real, target)
	if err != nil {
		unix.Unlinkat(tfd, it.ID, unix.AT_REMOVEDIR)
		return it, err
	}
	err = renameNoReplace(pfd, base, ifd, it.Name)
	unix.Close(pfd)
	if err != nil {
		unix.Unlinkat(tfd, it.ID, unix.AT_REMOVEDIR)
		if errors.Is(err, unix.EXDEV) {
			return it, errCrossDevice
		}
		return it, uerr("It could not be moved to the trash.")
	}
	mb, _ := json.Marshal(it)
	jfd, err := unix.Openat(tfd, it.ID+".json", unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return it, err
	}
	jf := os.NewFile(uintptr(jfd), it.ID+".json")
	_, err = jf.Write(mb)
	jf.Close()
	return it, err
}

func friendlyTrash(err error) error {
	var ue userError
	if errors.As(err, &ue) {
		return err
	}
	return uerr("The trash of this place could not be used.")
}

// readItem reads one item of an open trash: metadata from the JSON (path, time, size only), the
// name and kind from the item folder itself.
func readItem(tfd int, id string) (trashItem, error) {
	if !trashIDRe.MatchString(id) {
		return trashItem{}, uerr("This item is not in the trash.")
	}
	var it trashItem
	jfd, err := unix.Openat(tfd, id+".json", unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return it, uerr("This item is not in the trash.")
	}
	jf := os.NewFile(uintptr(jfd), id+".json")
	b, err := io.ReadAll(io.LimitReader(jf, 64<<10))
	jf.Close()
	if err != nil || json.Unmarshal(b, &it) != nil {
		return it, uerr("This trash item is damaged.")
	}
	ifd, err := openItemDir(tfd, id)
	if err != nil {
		return it, err
	}
	defer unix.Close(ifd)
	name, dir, err := itemName(ifd)
	if err != nil {
		return it, err
	}
	it.ID, it.Name, it.Dir = id, name, dir
	return it, nil
}

func listTrash(roots []rootInfo) []trashItem {
	out := []trashItem{}
	for _, r := range roots {
		tfd, err := openTrash(r, false)
		if err != nil || tfd < 0 {
			continue
		}
		f := os.NewFile(uintptr(tfd), trashDirName)
		names, _ := f.Readdirnames(-1)
		for _, n := range names {
			id, ok := strings.CutSuffix(n, ".json")
			if !ok || !trashIDRe.MatchString(id) {
				continue
			}
			if it, err := readItem(tfd, id); err == nil {
				it.Root = r.path
				out = append(out, it)
			}
		}
		f.Close()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Deleted > out[j].Deleted })
	return out
}

func (h *helperServer) trashRoot(root string) (rootInfo, error) {
	roots, _ := h.roots()
	for _, r := range roots {
		if r.path == root {
			return r, nil
		}
	}
	return rootInfo{}, uerr("This item is not in the trash.")
}

func removeTrashItem(tfd int, id string) error {
	if !trashIDRe.MatchString(id) {
		return uerr("This item is not in the trash.")
	}
	if ifd, err := openItemDir(tfd, id); err == nil { // only Box's own item folders
		unix.Close(ifd)
		if err := removeAllAt(tfd, id); err != nil {
			return err
		}
	}
	return removeAllAt(tfd, id+".json")
}

func (h *helperServer) trashList(req helperRequest, _ func(string)) (any, error) {
	roots, _ := h.roots()
	return map[string]any{"items": listTrash(roots)}, nil
}

// trashRestore moves an item back to where it was (or next to it as "<name> (restored)").
func (h *helperServer) trashRestore(req helperRequest, _ func(string)) (any, error) {
	r, err := h.trashRoot(req.Path)
	if err != nil {
		return nil, err
	}
	tfd, err := openTrash(r, false)
	if err != nil || tfd < 0 {
		return nil, uerr("This item is not in the trash.")
	}
	defer unix.Close(tfd)
	it, err := readItem(tfd, req.Name)
	if err != nil {
		return nil, err
	}
	// The folder it came from: an absolute path that passes the normal rules, in the same root.
	if !filepath.IsAbs(it.Path) || filepath.Clean(it.Path) != it.Path {
		return nil, uerr("This trash item is damaged.")
	}
	parent, pr, err := h.resolve(filepath.Dir(it.Path), true)
	if err != nil || pr.real != r.real {
		return nil, uerr("The folder it came from is gone. Create %s again first.", filepath.Dir(it.Path))
	}
	// Opened from the root one folder at a time, so a link swapped in after resolve cannot lead out.
	pfd, _, err := openBeneath(r.real, filepath.Join(parent, "x"))
	if err != nil {
		return nil, err
	}
	defer unix.Close(pfd)
	dest := it.Name
	for n := 1; ; n++ {
		var st unix.Stat_t
		if unix.Fstatat(pfd, dest, &st, unix.AT_SYMLINK_NOFOLLOW) != nil {
			break
		}
		ext := filepath.Ext(it.Name)
		base := strings.TrimSuffix(it.Name, ext)
		if it.Dir {
			base, ext = it.Name, ""
		}
		suffix := " (restored)"
		if n > 1 {
			suffix = " (restored " + strconv.Itoa(n) + ")"
		}
		dest = base + suffix + ext
	}
	if dest != filepath.Base(dest) || checkName(dest) != nil {
		return nil, uerr("This trash item is damaged.")
	}
	ifd, err := openItemDir(tfd, it.ID)
	if err != nil {
		return nil, err
	}
	err = renameNoReplace(ifd, it.Name, pfd, dest) // never overwrites a file planted meanwhile
	unix.Close(ifd)
	if errors.Is(err, unix.EEXIST) {
		return nil, uerr("%s exists again in that folder. Move it away and restore again.", dest)
	}
	if err != nil {
		return nil, uerr("It could not be restored.")
	}
	removeTrashItem(tfd, it.ID)
	return map[string]string{"path": filepath.Join(filepath.Dir(it.Path), dest)}, nil
}

func (h *helperServer) trashDelete(req helperRequest, _ func(string)) (any, error) {
	r, err := h.trashRoot(req.Path)
	if err != nil {
		return nil, err
	}
	tfd, err := openTrash(r, false)
	if err != nil || tfd < 0 {
		return nil, uerr("This item is not in the trash.")
	}
	defer unix.Close(tfd)
	if _, err := readItem(tfd, req.Name); err != nil {
		return nil, err
	}
	if err := removeTrashItem(tfd, req.Name); err != nil {
		return nil, uerr("It could not be deleted completely.")
	}
	return map[string]bool{"ok": true}, nil
}

// trashPurge deletes items older than req.Offset days (0 = everything: "Empty trash").
func (h *helperServer) trashPurge(req helperRequest, _ func(string)) (any, error) {
	roots, _ := h.roots()
	cut := time.Now().Add(-time.Duration(req.Offset) * 24 * time.Hour).Unix()
	n := 0
	for _, r := range roots {
		tfd, err := openTrash(r, false)
		if err != nil || tfd < 0 {
			continue
		}
		for _, it := range listTrash([]rootInfo{r}) {
			if req.Offset > 0 && it.Deleted > cut {
				continue
			}
			if removeTrashItem(tfd, it.ID) == nil {
				n++
			}
		}
		unix.Close(tfd)
	}
	return map[string]int{"deleted": n}, nil
}

// ---- web ----

const trashDaysKey = "trash_days"

func (a *App) trashDays() int {
	n, err := strconv.Atoi(a.store.setting(trashDaysKey))
	if err != nil || n < 1 || n > 365 {
		return 30
	}
	return n
}

func (a *App) trashGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	raw, err := a.helper.call(ctx, helperRequest{Action: "trash_list"}, nil)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, friendly(err))
		return
	}
	var res map[string]any
	json.Unmarshal(raw, &res)
	res["days"] = a.trashDays()
	writeJSON(w, http.StatusOK, res)
}

type trashBody struct {
	Root string `json:"root"`
	ID   string `json:"id"`
	Days int    `json:"days"`
}

func (a *App) trashAction(action, audit string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in trashBody
		if err := readJSON(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		// Path2 only names the item in the audit entry (the helper ignores it here).
		a.filesCall(w, r, helperRequest{Action: action, Path: in.Root, Name: in.ID, Path2: in.ID}, audit)
	}
}

func (a *App) trashEmpty(w http.ResponseWriter, r *http.Request) {
	a.filesCall(w, r, helperRequest{Action: "trash_purge", Offset: 0}, "files.trash_empty")
}

func (a *App) trashSettings(w http.ResponseWriter, r *http.Request) {
	var in trashBody
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Days < 1 || in.Days > 365 {
		writeError(w, http.StatusUnprocessableEntity, "Keep deleted items between 1 and 365 days.")
		return
	}
	a.store.setSetting(trashDaysKey, strconv.Itoa(in.Days))
	a.audit(r, "files.trash_days", strconv.Itoa(in.Days))
	writeJSON(w, http.StatusOK, map[string]int{"days": in.Days})
}

// watchTrash purges old items once a day (and an hour after start).
func (a *App) watchTrash() {
	time.Sleep(time.Hour)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		if raw, err := a.helper.call(ctx, helperRequest{Action: "trash_purge", Offset: int64(a.trashDays())}, nil); err == nil {
			var res struct{ Deleted int }
			if json.Unmarshal(raw, &res) == nil && res.Deleted > 0 {
				a.store.audit("trash", "", "files.trash_purge", strconv.Itoa(res.Deleted)+" items")
			}
		}
		cancel()
		time.Sleep(24 * time.Hour)
	}
}
