package main

// File explorer, root side. Every path is resolved (symlinks followed) and must end up inside
// one of the allowed roots, never in a system folder. Reads and uploads hand an open file
// descriptor to the web process (SCM_RIGHTS), so file contents never travel through JSON.

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/shirou/gopsutil/v4/disk"
)

type filesConfig struct {
	Roots       []string `json:"roots"`
	AllowSystem bool     `json:"allow_system"` // only settable by editing the file on the server
}

// blockedDirs are refused unless allow_system is set in /etc/nexovirt-box/files.json.
var blockedDirs = []string{"/etc", "/proc", "/sys", "/dev", "/boot", "/root", "/run", "/var/lib/nexovirt-box/web"}

func defaultRoots(appsDir string) []string {
	roots := []string{"/home", "/srv", appsDir}
	if parts, err := disk.Partitions(false); err == nil {
		for _, p := range parts {
			m := p.Mountpoint
			if m == "/" || underAny(m, systemDirs) || underAny(m, blockedDirs) || m == "/boot/efi" {
				continue
			}
			roots = append(roots, m)
		}
	}
	return roots
}

func (h *helperServer) filesConfig() filesConfig {
	var c filesConfig
	if !readJSONFile(fwPath("files.json"), &c) || len(c.Roots) == 0 {
		c.Roots = defaultRoots(h.appsDir)
	}
	return c
}

type rootInfo struct{ path, real string }

func (h *helperServer) roots() ([]rootInfo, filesConfig) {
	cfg := h.filesConfig()
	var out []rootInfo
	for _, r := range cfg.Roots {
		real, err := filepath.EvalSymlinks(r)
		if err == nil {
			out = append(out, rootInfo{r, real})
		}
	}
	return out, cfg
}

var errOutside = userError{"This folder is outside the folders Box may show. Add it in Settings."}

// resolve checks p (absolute, clean) and returns its real path. For a path that does not exist
// yet (new folder, upload target) the parent is resolved instead.
func (h *helperServer) resolve(p string, mustExist bool) (string, rootInfo, error) {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return "", rootInfo{}, uerr("The path is not valid.")
	}
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		if mustExist || !errors.Is(err, fs.ErrNotExist) {
			return "", rootInfo{}, uerr("This file or folder does not exist.")
		}
		parent, perr := filepath.EvalSymlinks(filepath.Dir(p))
		if perr != nil {
			return "", rootInfo{}, uerr("The folder does not exist.")
		}
		real = filepath.Join(parent, filepath.Base(p))
	}
	roots, cfg := h.roots()
	if !cfg.AllowSystem && (underAny(real, blockedDirs) || underAny(p, blockedDirs)) {
		return "", rootInfo{}, errOutside
	}
	for _, r := range roots {
		if real == r.real || strings.HasPrefix(real, strings.TrimSuffix(r.real, "/")+"/") {
			return real, r, nil
		}
	}
	return "", rootInfo{}, errOutside
}

type fileEntry struct {
	Name    string `json:"name"`
	Dir     bool   `json:"dir"`
	Size    int64  `json:"size"`
	ModTime int64  `json:"mtime"`
	Mode    string `json:"mode"`
	Link    bool   `json:"link,omitempty"`
}

func (h *helperServer) filesList(req helperRequest, logf func(string)) (any, error) {
	roots, _ := h.roots()
	if req.Path == "" {
		out := []string{}
		for _, r := range roots {
			out = append(out, r.path)
		}
		return map[string]any{"roots": out}, nil
	}
	real, _, err := h.resolve(req.Path, true)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(real)
	if err != nil {
		return nil, uerr("This folder cannot be read.")
	}
	list := []fileEntry{}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			continue
		}
		fe := fileEntry{Name: e.Name(), Dir: info.IsDir(), Size: info.Size(), ModTime: info.ModTime().Unix(), Mode: info.Mode().Perm().String()}
		if info.Mode()&fs.ModeSymlink != 0 {
			fe.Link = true
			if st, err := os.Stat(filepath.Join(real, e.Name())); err == nil {
				fe.Dir, fe.Size = st.IsDir(), st.Size()
			}
		}
		list = append(list, fe)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Dir != list[j].Dir {
			return list[i].Dir
		}
		return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name)
	})
	if len(list) > 5000 {
		list = list[:5000]
	}
	return map[string]any{"path": req.Path, "entries": list}, nil
}

var nameRe = regexp.MustCompile(`^[^/\x00]{1,255}$`)

func checkName(p string) error {
	b := filepath.Base(p)
	if !nameRe.MatchString(b) || b == "." || b == ".." {
		return uerr("The name is not valid.")
	}
	return nil
}

// ownLikeParent gives new files the owner of their folder (Box runs as root).
func ownLikeParent(p string) {
	if st, err := os.Stat(filepath.Dir(p)); err == nil {
		if s, ok := st.Sys().(*syscall.Stat_t); ok {
			os.Lchown(p, int(s.Uid), int(s.Gid))
		}
	}
}

func (h *helperServer) filesMkdir(req helperRequest, logf func(string)) (any, error) {
	if err := checkName(req.Path); err != nil {
		return nil, err
	}
	real, _, err := h.resolve(req.Path, false)
	if err != nil {
		return nil, err
	}
	if err := os.Mkdir(real, 0o755); err != nil {
		if errors.Is(err, fs.ErrExist) {
			return nil, uerr("Something with this name already exists.")
		}
		return nil, uerr("The folder could not be created.")
	}
	ownLikeParent(real)
	return map[string]bool{"ok": true}, nil
}

func (h *helperServer) filesRename(req helperRequest, logf func(string)) (any, error) {
	if err := checkName(req.Path2); err != nil {
		return nil, err
	}
	from, r, err := h.resolve(req.Path, true)
	if err != nil {
		return nil, err
	}
	if from == r.real {
		return nil, uerr("A top folder cannot be renamed or moved.")
	}
	to, _, err := h.resolve(req.Path2, false)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(to); err == nil {
		return nil, uerr("Something with this name already exists there.")
	}
	if strings.HasPrefix(to, from+"/") {
		return nil, uerr("A folder cannot be moved into itself.")
	}
	if err := os.Rename(from, to); err != nil {
		return nil, uerr("It could not be moved: %v", errors.Unwrap(err))
	}
	return map[string]bool{"ok": true}, nil
}

func (h *helperServer) filesDelete(req helperRequest, logf func(string)) (any, error) {
	// Resolve the parent, so a symlink itself is deleted, never what it points to.
	if err := checkName(req.Path); err != nil {
		return nil, err
	}
	parent, r, err := h.resolve(filepath.Dir(req.Path), true)
	if err != nil {
		return nil, err
	}
	target := filepath.Join(parent, filepath.Base(req.Path))
	if target == r.real || !strings.HasPrefix(target, strings.TrimSuffix(r.real, "/")+"/") {
		return nil, uerr("A top folder cannot be deleted.")
	}
	if _, err := os.Lstat(target); err != nil {
		return nil, uerr("This file or folder does not exist.")
	}
	if err := os.RemoveAll(target); err != nil {
		return nil, uerr("It could not be deleted completely.")
	}
	return map[string]bool{"ok": true}, nil
}

// filesOpenRead opens a regular file (not following a final symlink) for the web process.
func (h *helperServer) filesOpenRead(req helperRequest) (any, *os.File, error) {
	real, _, err := h.resolve(req.Path, true)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.OpenFile(real, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return nil, nil, uerr("This file cannot be read.")
	}
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		f.Close()
		return nil, nil, uerr("Only files can be downloaded.")
	}
	return map[string]any{"size": st.Size(), "name": filepath.Base(real), "mtime": st.ModTime().Unix()}, f, nil
}

var uploadIDRe = regexp.MustCompile(`^[a-f0-9]{16}$`)

func uploadTemp(target, id string) string {
	return filepath.Join(filepath.Dir(target), ".nxbox-upload-"+id)
}

// filesOpenWrite opens the temp file of an upload (created on the first chunk).
func (h *helperServer) filesOpenWrite(req helperRequest) (any, *os.File, error) {
	if err := checkName(req.Path); err != nil {
		return nil, nil, err
	}
	if !uploadIDRe.MatchString(req.ID) {
		return nil, nil, uerr("The upload is not valid.")
	}
	real, _, err := h.resolve(req.Path, false)
	if err != nil {
		return nil, nil, err
	}
	flags := os.O_WRONLY | os.O_CREATE | syscall.O_NOFOLLOW
	if req.Offset == 0 {
		flags |= os.O_TRUNC
	}
	f, err := os.OpenFile(uploadTemp(real, req.ID), flags, 0o644)
	if err != nil {
		return nil, nil, uerr("The file cannot be written here.")
	}
	return map[string]bool{"ok": true}, f, nil
}

func (h *helperServer) filesUploadFinish(req helperRequest, logf func(string)) (any, error) {
	if err := checkName(req.Path); err != nil {
		return nil, err
	}
	if !uploadIDRe.MatchString(req.ID) {
		return nil, uerr("The upload is not valid.")
	}
	real, _, err := h.resolve(req.Path, false)
	if err != nil {
		return nil, err
	}
	tmp := uploadTemp(real, req.ID)
	if req.Op == "abort" {
		os.Remove(tmp)
		return map[string]bool{"ok": true}, nil
	}
	if st, err := os.Lstat(real); err == nil {
		if !req.Overwrite || st.IsDir() {
			os.Remove(tmp)
			return nil, uerr("%s already exists.", filepath.Base(real))
		}
	}
	if err := os.Rename(tmp, real); err != nil {
		os.Remove(tmp)
		return nil, uerr("The upload could not be saved.")
	}
	ownLikeParent(real)
	return map[string]bool{"ok": true}, nil
}

// filesRootsSet changes the shown folders. System folders stay refused here; they can only be
// allowed by editing /etc/nexovirt-box/files.json on the server.
func (h *helperServer) filesRootsSet(req helperRequest, logf func(string)) (any, error) {
	cfg := h.filesConfig()
	var roots []string
	for _, r := range strings.Split(req.Path, "\n") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}
		if !filepath.IsAbs(r) || filepath.Clean(r) != r || r == "/" {
			return nil, uerr("%s is not a full folder path.", r)
		}
		real, err := filepath.EvalSymlinks(r)
		if err != nil {
			return nil, uerr("%s does not exist.", r)
		}
		if !cfg.AllowSystem && (underAny(r, blockedDirs) || underAny(real, blockedDirs) || real == "/") {
			return nil, uerr("%s is a system folder and cannot be shown.", r)
		}
		roots = append(roots, r)
	}
	if len(roots) == 0 {
		roots = defaultRoots(h.appsDir)
	}
	cfg.Roots = roots
	if err := writeJSONFile(fwPath("files.json"), cfg); err != nil {
		return nil, err
	}
	return map[string]any{"roots": roots}, nil
}
