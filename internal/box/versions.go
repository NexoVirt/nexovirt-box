package box

// File versions: when an upload replaces a file in the file explorer, the old file moves into
// <root>/.nxbox-versions/<id>/file (with <id>.json: which path, when). The last 10 versions per
// file are kept, none older than 30 days. "Restore" makes the current file a version too, so a
// restore can always be undone. Changes made outside Box are not versioned. The folders get the
// same protection as the trash (root-owned 0700, opened without following links, every move
// relative to descriptors; metadata JSON untrusted).

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const (
	versionsDirName = ".nxbox-versions"
	versionsKeep    = 10
	versionsMaxAge  = 30 * 24 * time.Hour
	versionFileName = "file"
)

type fileVersion struct {
	ID    string `json:"id"`
	Path  string `json:"path"` // real path of the file
	Saved int64  `json:"saved"`
	Size  int64  `json:"size"`
}

// keepVersion moves the current file at real (inside r) into the versions folder. Only regular
// files are versioned.
func keepVersion(r rootInfo, real, spare string) error {
	info, err := os.Lstat(real)
	if err != nil || !info.Mode().IsRegular() {
		return nil
	}
	vfd, err := openOwnDir(r, versionsDirName, true)
	if err != nil {
		return err
	}
	defer unix.Close(vfd)
	b := make([]byte, 6)
	rand.Read(b)
	v := fileVersion{ID: strconv.FormatInt(time.Now().Unix(), 10) + "-" + hex.EncodeToString(b), Path: real, Saved: time.Now().Unix(), Size: info.Size()}
	if err := unix.Mkdirat(vfd, v.ID, 0o700); err != nil {
		return err
	}
	ifd, err := openItemDir(vfd, v.ID)
	if err != nil {
		return err
	}
	defer unix.Close(ifd)
	pfd, base, err := openBeneath(r.real, real)
	if err != nil {
		unix.Unlinkat(vfd, v.ID, unix.AT_REMOVEDIR)
		return err
	}
	err = renameNoReplace(pfd, base, ifd, versionFileName)
	unix.Close(pfd)
	if err != nil {
		unix.Unlinkat(vfd, v.ID, unix.AT_REMOVEDIR)
		return err
	}
	mb, _ := json.Marshal(v)
	if err := writeAt(vfd, v.ID+".json", mb); err != nil {
		return err
	}
	pruneVersions(vfd, real, spare)
	return nil
}

func writeAt(dirFD int, name string, b []byte) error {
	fd, err := unix.Openat(dirFD, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return err
	}
	f := os.NewFile(uintptr(fd), name)
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}

// readVersions lists the versions in an open versions folder (all paths, or only path).
func readVersions(vfd int, path string) []fileVersion {
	names, _ := readDirNamesFD(vfd)
	var out []fileVersion
	for _, n := range names {
		id, ok := strings.CutSuffix(n, ".json")
		if !ok || !trashIDRe.MatchString(id) {
			continue
		}
		jfd, err := unix.Openat(vfd, n, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			continue
		}
		jf := os.NewFile(uintptr(jfd), n)
		b, _ := io.ReadAll(io.LimitReader(jf, 64<<10))
		jf.Close()
		var v fileVersion
		if json.Unmarshal(b, &v) != nil || (path != "" && v.Path != path) {
			continue
		}
		v.ID = id // never trust the id inside the JSON
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Saved > out[j].Saved })
	return out
}

// pruneVersions keeps the newest versionsKeep versions of path and drops older than the age.
// spare is a version being restored right now (never pruned).
func pruneVersions(vfd int, path, spare string) {
	cut := time.Now().Add(-versionsMaxAge).Unix()
	for i, v := range readVersions(vfd, path) {
		if v.ID != spare && (i >= versionsKeep || v.Saved < cut) {
			removeTrashItem(vfd, v.ID)
		}
	}
}

func (h *helperServer) versionTarget(path string) (string, rootInfo, error) {
	if err := checkName(path); err != nil {
		return "", rootInfo{}, err
	}
	parent, r, err := h.resolve(filepath.Dir(path), true)
	if err != nil {
		return "", r, err
	}
	return filepath.Join(parent, filepath.Base(path)), r, nil
}

func (h *helperServer) versionsList(req helperRequest, _ func(string)) (any, error) {
	real, r, err := h.versionTarget(req.Path)
	if err != nil {
		return nil, err
	}
	vfd, err := openOwnDir(r, versionsDirName, false)
	if err != nil || vfd < 0 {
		return map[string]any{"versions": []fileVersion{}}, nil
	}
	defer unix.Close(vfd)
	vs := readVersions(vfd, real)
	if vs == nil {
		vs = []fileVersion{}
	}
	return map[string]any{"versions": vs}, nil
}

// versionRestore puts a version back; the current file becomes a version first.
func (h *helperServer) versionRestore(req helperRequest, _ func(string)) (any, error) {
	real, r, err := h.versionTarget(req.Path)
	if err != nil {
		return nil, err
	}
	if !trashIDRe.MatchString(req.Name) {
		return nil, uerr("This version does not exist.")
	}
	vfd, err := openOwnDir(r, versionsDirName, false)
	if err != nil || vfd < 0 {
		return nil, uerr("This version does not exist.")
	}
	defer unix.Close(vfd)
	found := false
	for _, v := range readVersions(vfd, real) {
		found = found || v.ID == req.Name
	}
	if !found {
		return nil, uerr("This version does not exist.")
	}
	ifd, err := openItemDir(vfd, req.Name)
	if err != nil {
		return nil, err
	}
	defer unix.Close(ifd)
	if name, dir, err := itemName(ifd); err != nil || dir || name != versionFileName {
		return nil, uerr("This version is damaged.")
	}
	if err := keepVersion(r, real, req.Name); err != nil {
		return nil, uerr("The current file could not be kept as a version, so nothing was restored.")
	}
	pfd, base, err := openBeneath(r.real, real)
	if err != nil {
		return nil, err
	}
	defer unix.Close(pfd)
	if err := renameNoReplace(ifd, versionFileName, pfd, base); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return nil, uerr("%s was changed meanwhile. Try again.", base)
		}
		return nil, uerr("The version could not be restored.")
	}
	removeTrashItem(vfd, req.Name)
	return map[string]bool{"ok": true}, nil
}

// versionsPurge drops versions older than the age in every root (daily, with the trash purge).
func (h *helperServer) versionsPurge() {
	roots, _ := h.roots()
	cut := time.Now().Add(-versionsMaxAge).Unix()
	for _, r := range roots {
		vfd, err := openOwnDir(r, versionsDirName, false)
		if err != nil || vfd < 0 {
			continue
		}
		for _, v := range readVersions(vfd, "") {
			if v.Saved < cut {
				removeTrashItem(vfd, v.ID)
			}
		}
		unix.Close(vfd)
	}
}

// ---- web ----

func (a *App) versionsGet(w http.ResponseWriter, r *http.Request) {
	a.filesCall(w, r, helperRequest{Action: "versions_list", Path: r.URL.Query().Get("path")}, "")
}

func (a *App) versionRestoreWeb(w http.ResponseWriter, r *http.Request) {
	var in struct{ Path, ID string }
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.filesCall(w, r, helperRequest{Action: "version_restore", Path: in.Path, Name: in.ID, Path2: in.ID}, "files.version_restore")
}
