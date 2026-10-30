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
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
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

func trashOf(r rootInfo) string { return filepath.Join(r.real, trashDirName) }

func inTrash(p string) bool {
	return strings.Contains(p+"/", "/"+trashDirName+"/")
}

// moveToTrash moves target (inside root r) into r's trash. errCrossDevice means the item lies on
// another file system than the root (a mount inside it) and can only be deleted for good.
var errCrossDevice = userError{"needs_permanent: This item is on another drive than its folder's trash, so it can only be deleted for good."}

func moveToTrash(r rootInfo, target, shown string) (trashItem, error) {
	info, err := os.Lstat(target)
	if err != nil {
		return trashItem{}, uerr("This file or folder does not exist.")
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
	dir := filepath.Join(trashOf(r), it.ID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return it, err
	}
	if err := os.Rename(target, filepath.Join(dir, it.Name)); err != nil {
		os.Remove(dir)
		if errors.Is(err, syscall.EXDEV) {
			return it, errCrossDevice
		}
		return it, uerr("It could not be moved to the trash.")
	}
	mb, _ := json.Marshal(it)
	if err := os.WriteFile(filepath.Join(trashOf(r), it.ID+".json"), mb, 0o600); err != nil {
		return it, err
	}
	return it, nil
}

func listTrash(roots []rootInfo) []trashItem {
	out := []trashItem{}
	for _, r := range roots {
		files, _ := filepath.Glob(filepath.Join(trashOf(r), "*.json"))
		for _, f := range files {
			var it trashItem
			b, err := os.ReadFile(f)
			if err != nil || json.Unmarshal(b, &it) != nil || !trashIDRe.MatchString(it.ID) || it.ID+".json" != filepath.Base(f) {
				continue
			}
			it.Root = r.path
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Deleted > out[j].Deleted })
	return out
}

// trashFind returns the root and the item for a root path and an id.
func (h *helperServer) trashFind(root, id string) (rootInfo, trashItem, error) {
	if !trashIDRe.MatchString(id) {
		return rootInfo{}, trashItem{}, uerr("This item is not in the trash.")
	}
	roots, _ := h.roots()
	for _, r := range roots {
		if r.path != root {
			continue
		}
		for _, it := range listTrash([]rootInfo{r}) {
			if it.ID == id {
				return r, it, nil
			}
		}
	}
	return rootInfo{}, trashItem{}, uerr("This item is not in the trash.")
}

func removeTrashItem(r rootInfo, id string) error {
	if err := os.RemoveAll(filepath.Join(trashOf(r), id)); err != nil {
		return err
	}
	return os.Remove(filepath.Join(trashOf(r), id+".json"))
}

func (h *helperServer) trashList(req helperRequest, _ func(string)) (any, error) {
	roots, _ := h.roots()
	return map[string]any{"items": listTrash(roots)}, nil
}

// trashRestore moves an item back to where it was (or next to it as "<name> (restored)").
func (h *helperServer) trashRestore(req helperRequest, _ func(string)) (any, error) {
	r, it, err := h.trashFind(req.Path, req.Name)
	if err != nil {
		return nil, err
	}
	parent, pr, err := h.resolve(filepath.Dir(it.Path), true)
	if err != nil || pr.real != r.real {
		return nil, uerr("The folder it came from is gone. Create %s again first.", filepath.Dir(it.Path))
	}
	dest := filepath.Join(parent, it.Name)
	for n := 1; exists(dest) || isLink(dest); n++ {
		ext := filepath.Ext(it.Name)
		base := strings.TrimSuffix(it.Name, ext)
		if it.Dir {
			base, ext = it.Name, ""
		}
		dest = filepath.Join(parent, base+" (restored"+map[bool]string{true: "", false: " " + strconv.Itoa(n)}[n == 1]+")"+ext)
	}
	if err := os.Rename(filepath.Join(trashOf(r), it.ID, it.Name), dest); err != nil {
		return nil, uerr("It could not be restored.")
	}
	removeTrashItem(r, it.ID)
	return map[string]string{"path": filepath.Join(filepath.Dir(it.Path), filepath.Base(dest))}, nil
}

func isLink(p string) bool { _, err := os.Lstat(p); return err == nil }

func (h *helperServer) trashDelete(req helperRequest, _ func(string)) (any, error) {
	r, it, err := h.trashFind(req.Path, req.Name)
	if err != nil {
		return nil, err
	}
	if err := removeTrashItem(r, it.ID); err != nil {
		return nil, uerr("It could not be deleted completely.")
	}
	return map[string]bool{"ok": true}, nil
}

// trashPurge deletes items older than req.Offset days (0 = everything: "Empty trash").
func (h *helperServer) trashPurge(req helperRequest, _ func(string)) (any, error) {
	roots, _ := h.roots()
	cut := time.Now().Add(-time.Duration(req.Offset) * 24 * time.Hour).Unix()
	n := 0
	for _, it := range listTrash(roots) {
		if req.Offset > 0 && it.Deleted > cut {
			continue
		}
		for _, r := range roots {
			if r.path == it.Root && removeTrashItem(r, it.ID) == nil {
				n++
			}
		}
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
