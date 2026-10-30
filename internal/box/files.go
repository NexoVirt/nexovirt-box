package box

// File explorer, root side. Every path is resolved (symlinks followed) and must end up inside
// one of the allowed roots, never in a system folder. Reads and uploads hand an open file
// descriptor to the web process (SCM_RIGHTS), so file contents never travel through JSON.

import (
	"archive/zip"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

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
	if r, ok := visibleIn(p, real, roots, cfg); ok {
		return real, r, nil
	}
	return "", rootInfo{}, errOutside
}

// visibleIn is THE path rule of the file features: a path (as given and resolved) may be shown
// when it is not under a blocked folder (unless allow_system) and lies inside an allowed root.
// Browsing, search and every other walker over the roots use it, so they cannot drift apart.
func visibleIn(p, real string, roots []rootInfo, cfg filesConfig) (rootInfo, bool) {
	if inTrash(p) || inTrash(real) { // only the trash actions reach it (trash.go)
		return rootInfo{}, false
	}
	if !cfg.AllowSystem && (underAny(real, blockedDirs) || underAny(p, blockedDirs)) {
		return rootInfo{}, false
	}
	for _, r := range roots {
		if real == r.real || strings.HasPrefix(real, strings.TrimSuffix(r.real, "/")+"/") {
			return r, true
		}
	}
	return rootInfo{}, false
}

// walkRoots returns the roots a walker may start from (each root itself must be visible) and
// the predicate for every path it reports. Walkers never follow symlinks.
func (h *helperServer) walkRoots() ([]string, func(string) bool) {
	roots, cfg := h.roots()
	ok := func(p string) bool { _, v := visibleIn(p, p, roots, cfg); return v }
	var out []string
	for _, r := range roots {
		if _, v := visibleIn(r.path, r.real, roots, cfg); v {
			out = append(out, r.real)
		}
	}
	return out, ok
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
		return map[string]any{"roots": out, "places": placesFor(roots)}, nil
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
		if err != nil || e.Name() == trashDirName {
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
		if errors.Is(err, fs.ErrExist) && req.Op == "exist_ok" {
			if st, serr := os.Lstat(real); serr == nil && st.IsDir() {
				return map[string]bool{"ok": true}, nil
			}
		}
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
	if req.Op != "permanent" {
		it, err := moveToTrash(r, target, req.Path)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "trash": it.ID}, nil
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

// filesCopy copies a file or folder (Path) to a new path (Path2). Symlinks are copied as links,
// never followed, so a copy can never pull in content from outside the allowed folders.
func (h *helperServer) filesCopy(req helperRequest, logf func(string)) (any, error) {
	if err := checkName(req.Path2); err != nil {
		return nil, err
	}
	from, _, err := h.resolve(req.Path, true)
	if err != nil {
		return nil, err
	}
	to, _, err := h.resolve(req.Path2, false)
	if err != nil {
		return nil, err
	}
	if _, err := os.Lstat(to); err == nil {
		return nil, uerr("Something with this name already exists there.")
	}
	if to == from || strings.HasPrefix(to, from+"/") {
		return nil, uerr("A folder cannot be copied into itself.")
	}
	err = filepath.WalkDir(from, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		dst := filepath.Join(to, strings.TrimPrefix(p, from))
		info, err := d.Info()
		if err != nil {
			return err
		}
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			link, err := os.Readlink(p)
			if err != nil {
				return err
			}
			return os.Symlink(link, dst)
		case d.IsDir():
			return os.Mkdir(dst, info.Mode().Perm())
		case info.Mode().IsRegular():
			return copyTreeFile(p, dst, info.Mode().Perm())
		}
		return nil // sockets, devices and pipes are skipped
	})
	if err != nil {
		return nil, uerr("It could not be copied completely: %v", errors.Unwrap(err))
	}
	ownLikeParent(to)
	return map[string]bool{"ok": true}, nil
}

func copyTreeFile(src, dst string, perm fs.FileMode) error {
	in, err := os.OpenFile(src, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, perm)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// filesSize adds up a folder (for the info panel). It stops after 20 s or 2 million entries.
func (h *helperServer) filesSize(req helperRequest, logf func(string)) (any, error) {
	real, _, err := h.resolve(req.Path, true)
	if err != nil {
		return nil, err
	}
	var size, files, dirs int64
	deadline := time.Now().Add(20 * time.Second)
	partial := false
	filepath.WalkDir(real, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if files+dirs > 2_000_000 || time.Now().After(deadline) {
			partial = true
			return filepath.SkipAll
		}
		if d.IsDir() {
			dirs++
			return nil
		}
		if info, err := d.Info(); err == nil && info.Mode().IsRegular() {
			size += info.Size()
			files++
		}
		return nil
	})
	return map[string]any{"size": size, "files": files, "dirs": max(dirs-1, 0), "partial": partial}, nil
}

// filesZip streams a zip of one or more paths (newline separated in Path) through a pipe.
// Symlinks are left out of the archive, so it only ever contains files inside the allowed folders.
func (h *helperServer) filesZip(req helperRequest) (any, *os.File, error) {
	var paths []string
	for _, p := range strings.Split(req.Path, "\n") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		real, _, err := h.resolve(p, true)
		if err != nil {
			return nil, nil, err
		}
		paths = append(paths, real)
	}
	if len(paths) == 0 || len(paths) > 1000 {
		return nil, nil, uerr("Choose what to download.")
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, nil, err
	}
	go func() {
		defer pw.Close()
		zw := zip.NewWriter(pw)
		defer zw.Close()
		for _, root := range paths {
			base := filepath.Dir(root)
			filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
				if err != nil || d.Type()&fs.ModeSymlink != 0 {
					return nil
				}
				name := strings.TrimPrefix(p, base+"/")
				info, err := d.Info()
				if err != nil {
					return nil
				}
				if d.IsDir() {
					_, err = zw.Create(name + "/")
					return err
				}
				if !info.Mode().IsRegular() {
					return nil
				}
				hdr, _ := zip.FileInfoHeader(info)
				hdr.Name, hdr.Method = name, zip.Deflate
				w, err := zw.CreateHeader(hdr)
				if err != nil {
					return err
				}
				f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
				if err != nil {
					return nil
				}
				_, err = io.Copy(w, f)
				f.Close()
				return err // a closed pipe (browser gone) stops the walk
			})
		}
	}()
	name := filepath.Base(paths[0])
	if len(paths) > 1 {
		name = filepath.Base(filepath.Dir(paths[0]))
	}
	return map[string]any{"name": name + ".zip"}, pr, nil
}

// placeInfo is a top folder with the usage of the drive it lives on (sidebar of the explorer).
type placeInfo struct {
	Path  string `json:"path"`
	Total uint64 `json:"total"`
	Used  uint64 `json:"used"`
	Drive bool   `json:"drive"` // its own mounted drive
}

func placesFor(roots []rootInfo) []placeInfo {
	mounts := map[string]bool{}
	if parts, err := disk.Partitions(false); err == nil {
		for _, p := range parts {
			mounts[p.Mountpoint] = true
		}
	}
	out := []placeInfo{}
	for _, r := range roots {
		pi := placeInfo{Path: r.path, Drive: mounts[r.real]}
		if u, err := disk.Usage(r.real); err == nil {
			pi.Total, pi.Used = u.Total, u.Used
		}
		out = append(out, pi)
	}
	return out
}
