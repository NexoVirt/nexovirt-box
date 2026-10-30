package box

// File explorer, root side. Every path is resolved (symlinks followed) and must end up inside
// one of the allowed roots, never in a system folder. Reads and uploads hand an open file
// descriptor to the web process (SCM_RIGHTS), so file contents never travel through JSON.

import (
	"archive/zip"
	"errors"
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
	"golang.org/x/sys/unix"
)

type filesConfig struct {
	Roots       []string `json:"roots"`
	AllowSystem bool     `json:"allow_system"` // only settable by editing the file on the server
	appsDir     string   // Box's app folders: only their data is shown (boxHidden)
}

// blockedDirs are refused unless allow_system is set in /etc/nexovirt-box/files.json. The file
// actions run as root, so every folder whose files root runs or trusts (programs, libraries,
// units, cron, package scripts, Docker's state) is in here, not only the secret ones.
var blockedDirs = []string{"/etc", "/proc", "/sys", "/dev", "/boot", "/root", "/run", "/usr", "/bin", "/sbin",
	"/lib", "/lib32", "/lib64", "/libx32", "/snap", "/var/lib/docker", "/var/lib/containerd", "/var/lib/dpkg",
	"/var/lib/apt", "/var/cache/apt", "/var/lib/systemd", "/var/lib/snapd", "/var/spool/cron", "/var/spool/anacron",
	"/var/lib/nexovirt-box/web"}

// boxHidden reports whether p is one of Box's own files: its web state, its backup staging and,
// in the app folders, everything but the apps' data. An app's compose.yaml, .env and app.json
// decide what runs as root, so editing them through the files would skip every check.
func boxHidden(p, appsDir string) bool {
	if appsDir == "" {
		return false
	}
	base := filepath.Dir(appsDir)
	for _, d := range []string{"web", "backup-tmp"} {
		if x := filepath.Join(base, d); p == x || within(p, x) {
			return true
		}
	}
	if !within(p, appsDir) {
		return false
	}
	rel, _ := filepath.Rel(appsDir, p)
	parts := strings.Split(filepath.ToSlash(rel), "/")
	return len(parts) > 1 && parts[1] != "data"
}

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
	c.appsDir = h.appsDir
	if real, err := filepath.EvalSymlinks(h.appsDir); err == nil {
		c.appsDir = real
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
	if boxHidden(real, cfg.appsDir) || boxHidden(p, cfg.appsDir) {
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
	cfg := h.filesConfig()
	entries, err := os.ReadDir(real)
	if err != nil {
		return nil, uerr("This folder cannot be read.")
	}
	list := []fileEntry{}
	for _, e := range entries {
		info, err := e.Info()
		if err != nil || e.Name() == trashDirName || e.Name() == versionsDirName || boxHidden(filepath.Join(real, e.Name()), cfg.appsDir) {
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

func (h *helperServer) filesMkdir(req helperRequest, logf func(string)) (any, error) {
	if err := checkName(req.Path); err != nil {
		return nil, err
	}
	real, r, err := h.resolve(req.Path, false)
	if err != nil {
		return nil, err
	}
	fd, name, err := beneath(r, real)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	if err := unix.Mkdirat(fd, name, 0o755); err != nil {
		if errors.Is(err, unix.EEXIST) && req.Op == "exist_ok" {
			if st, serr := lstatAt(fd, name); serr == nil && isDirMode(uint32(st.Mode)) {
				return map[string]bool{"ok": true}, nil
			}
		}
		if errors.Is(err, unix.EEXIST) {
			return nil, uerr("Something with this name already exists.")
		}
		return nil, uerr("The folder could not be created.")
	}
	ownLikeParentAt(fd, name)
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
	to, r2, err := h.resolve(req.Path2, false)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(to, from+"/") {
		return nil, uerr("A folder cannot be moved into itself.")
	}
	ffd, fname, err := beneath(r, from)
	if err != nil {
		return nil, err
	}
	defer unix.Close(ffd)
	tfd, tname, err := beneath(r2, to)
	if err != nil {
		return nil, err
	}
	defer unix.Close(tfd)
	if err := renameNoReplace(ffd, fname, tfd, tname); err != nil {
		if errors.Is(err, unix.EEXIST) {
			return nil, uerr("Something with this name already exists there.")
		}
		return nil, uerr("It could not be moved: %v", err)
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
	fd, name, err := beneath(r, target)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	if _, err := lstatAt(fd, name); err != nil {
		return nil, uerr("This file or folder does not exist.")
	}
	if req.Op != "permanent" {
		it, err := moveToTrash(r, target, req.Path)
		if err != nil {
			return nil, err
		}
		return map[string]any{"ok": true, "trash": it.ID}, nil
	}
	if err := removeAllAt(fd, name); err != nil {
		return nil, uerr("It could not be deleted completely.")
	}
	return map[string]bool{"ok": true}, nil
}

// filesOpenRead opens a regular file (not following a final symlink) for the web process.
func (h *helperServer) filesOpenRead(req helperRequest) (any, *os.File, error) {
	real, r, err := h.resolve(req.Path, true)
	if err != nil {
		return nil, nil, err
	}
	fd, name, err := beneath(r, real)
	if err != nil {
		return nil, nil, err
	}
	defer unix.Close(fd)
	f, err := openFileAt(fd, name)
	if err != nil {
		return nil, nil, uerr("Only files can be downloaded.")
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, nil, uerr("This file cannot be read.")
	}
	return map[string]any{"size": st.Size(), "name": filepath.Base(real), "mtime": st.ModTime().Unix()}, f, nil
}

var uploadIDRe = regexp.MustCompile(`^[a-f0-9]{16}$`)

func uploadTempName(id string) string { return ".nxbox-upload-" + id }

// filesOpenWrite opens the temp file of an upload (created on the first chunk).
func (h *helperServer) filesOpenWrite(req helperRequest) (any, *os.File, error) {
	if err := checkName(req.Path); err != nil {
		return nil, nil, err
	}
	if !uploadIDRe.MatchString(req.ID) {
		return nil, nil, uerr("The upload is not valid.")
	}
	real, r, err := h.resolve(req.Path, false)
	if err != nil {
		return nil, nil, err
	}
	dfd, _, err := beneath(r, real)
	if err != nil {
		return nil, nil, err
	}
	defer unix.Close(dfd)
	flags := unix.O_WRONLY | unix.O_CREAT | unix.O_NOFOLLOW | unix.O_NONBLOCK | unix.O_CLOEXEC
	if req.Offset == 0 {
		flags |= unix.O_TRUNC
	}
	fd, err := unix.Openat(dfd, uploadTempName(req.ID), flags, 0o644)
	if err != nil {
		return nil, nil, uerr("The file cannot be written here.")
	}
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG {
		unix.Close(fd)
		return nil, nil, uerr("The file cannot be written here.")
	}
	unix.SetNonblock(fd, false)
	return map[string]bool{"ok": true}, os.NewFile(uintptr(fd), "upload"), nil
}

func (h *helperServer) filesUploadFinish(req helperRequest, logf func(string)) (any, error) {
	if err := checkName(req.Path); err != nil {
		return nil, err
	}
	if !uploadIDRe.MatchString(req.ID) {
		return nil, uerr("The upload is not valid.")
	}
	real, r, err := h.resolve(req.Path, false)
	if err != nil {
		return nil, err
	}
	fd, name, err := beneath(r, real)
	if err != nil {
		return nil, err
	}
	defer unix.Close(fd)
	tmp := uploadTempName(req.ID)
	if req.Op == "abort" {
		unix.Unlinkat(fd, tmp, 0)
		return map[string]bool{"ok": true}, nil
	}
	replace := false
	if st, err := lstatAt(fd, name); err == nil {
		if !req.Overwrite || st.Mode&unix.S_IFMT != unix.S_IFREG {
			unix.Unlinkat(fd, tmp, 0)
			return nil, uerr("%s already exists.", name)
		}
		if err := keepVersion(r, real, ""); err != nil { // the replaced file stays as a version (versions.go)
			log.Printf("files: keep version of %s: %v", real, err)
		}
		replace = true
	}
	if replace {
		err = unix.Renameat(fd, tmp, fd, name)
	} else {
		err = renameNoReplace(fd, tmp, fd, name)
	}
	if err != nil {
		unix.Unlinkat(fd, tmp, 0)
		return nil, uerr("The upload could not be saved.")
	}
	ownLikeParentAt(fd, name)
	return map[string]bool{"ok": true}, nil
}

// filesRootsSet changes the shown folders. System folders stay refused here; they can only be
// allowed by editing /etc/nexovirt-box/files.json on the server.
func (h *helperServer) filesRootsSet(req helperRequest, logf func(string)) (any, error) {
	if !h.consoleAllowed(req) { // the shown folders decide what the file actions may change as root
		return nil, uerr("Enter your password to change the shown folders.")
	}
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
		if !cfg.AllowSystem && (underAny(r, blockedDirs) || underAny(real, blockedDirs) || real == "/" || boxHidden(real, cfg.appsDir)) {
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
	from, r, err := h.resolve(req.Path, true)
	if err != nil {
		return nil, err
	}
	to, r2, err := h.resolve(req.Path2, false)
	if err != nil {
		return nil, err
	}
	if to == from || strings.HasPrefix(to, from+"/") {
		return nil, uerr("A folder cannot be copied into itself.")
	}
	sfd, sname, err := beneath(r, from)
	if err != nil {
		return nil, err
	}
	defer unix.Close(sfd)
	dfd, dname, err := beneath(r2, to)
	if err != nil {
		return nil, err
	}
	defer unix.Close(dfd)
	if _, err := lstatAt(dfd, dname); err == nil {
		return nil, uerr("Something with this name already exists there.")
	}
	if err := copyAt(sfd, sname, dfd, dname); err != nil {
		return nil, uerr("It could not be copied completely: %v", err)
	}
	ownLikeParentAt(dfd, dname)
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
	real, r, err := h.resolve(req.Path, true)
	if err != nil {
		return nil, err
	}
	var size, files, dirs int64
	deadline := time.Now().Add(20 * time.Second)
	partial := false
	start, name, err := openStart(r, real)
	if err != nil {
		return nil, err
	}
	defer unix.Close(start)
	walkAt(start, name, "", func(_ int, _, _ string, st unix.Stat_t) error {
		if files+dirs > 2_000_000 || time.Now().After(deadline) {
			partial = true
			return errStopWalk
		}
		switch st.Mode & unix.S_IFMT {
		case unix.S_IFDIR:
			dirs++
		case unix.S_IFREG:
			size += st.Size
			files++
		}
		return nil
	})
	return map[string]any{"size": size, "files": files, "dirs": max(dirs-1, 0), "partial": partial}, nil
}

// filesZip streams a zip of one or more paths (newline separated in Path) through a pipe.
// Symlinks are left out of the archive, so it only ever contains files inside the allowed folders.
func (h *helperServer) filesZip(req helperRequest) (any, *os.File, error) {
	type zipItem struct {
		fd         int
		name, real string // real: the name shown in the zip
	}
	var paths []string
	var items []zipItem
	closeAll := func() {
		for _, it := range items {
			unix.Close(it.fd)
		}
	}
	for _, p := range strings.Split(req.Path, "\n") {
		if p = strings.TrimSpace(p); p == "" {
			continue
		}
		real, r, err := h.resolve(p, true)
		var fd int
		var name string
		if err == nil {
			fd, name, err = openStart(r, real)
		}
		if err != nil {
			closeAll()
			return nil, nil, err
		}
		items = append(items, zipItem{fd, name, filepath.Base(real)})
		paths = append(paths, real)
	}
	if len(paths) == 0 || len(paths) > 1000 {
		closeAll()
		return nil, nil, uerr("Choose what to download.")
	}
	pr, pw, err := os.Pipe()
	if err != nil {
		closeAll()
		return nil, nil, err
	}
	go func() {
		defer pw.Close()
		defer closeAll()
		zw := zip.NewWriter(pw)
		defer zw.Close()
		for _, it := range items {
			walkAt(it.fd, it.name, it.real, func(parent int, name, rel string, st unix.Stat_t) error {
				switch st.Mode & unix.S_IFMT {
				case unix.S_IFDIR:
					_, err := zw.Create(rel + "/")
					return err
				case unix.S_IFREG:
				default:
					return nil // links, sockets, devices and pipes are left out
				}
				f, err := openFileAt(parent, name)
				if err != nil {
					return nil
				}
				defer f.Close()
				info, err := f.Stat()
				if err != nil {
					return nil
				}
				hdr, _ := zip.FileInfoHeader(info)
				hdr.Name, hdr.Method = rel, zip.Deflate
				w, err := zw.CreateHeader(hdr)
				if err != nil {
					return err
				}
				_, err = io.Copy(w, f)
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
