package main

// Backup archives: one file per backup, `<app>-<UTC time>.nxb`, an age-encrypted (X25519)
// gzip tar. Inside:
//
//	manifest.json     what the archive holds
//	app/...           the app folder (compose.yaml, .env, app.json, data/)
//	datadir/...       a data folder chosen at install that lies outside the app folder
//	volumes/<key>/... the app's named Docker volumes (compose volume key)
//	box/...           Box's own settings (database, notification key) for the "box" archive
//
// Restores unpack into a fresh folder through os.Root, so a forged archive (anyone with the
// public key can make one) can never write outside it.

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
)

type backupManifest struct {
	Format     int      `json:"format"`
	App        string   `json:"app"`
	Name       string   `json:"name"`
	Created    int64    `json:"created"`
	BoxVersion string   `json:"box_version"`
	Custom     bool     `json:"custom,omitempty"`
	DataDir    string   `json:"data_dir,omitempty"` // data folder outside the app folder (in datadir/)
	Volumes    []string `json:"volumes,omitempty"`  // compose volume keys (in volumes/<key>/)
}

type archiveInfo struct {
	Name string `json:"name"`
	App  string `json:"app"`
	At   int64  `json:"at"`
	Size int64  `json:"size"`
}

var archiveRe = regexp.MustCompile(`^([a-z0-9][a-z0-9-]{0,62})-(\d{8}T\d{6}Z)\.nxb$`)

const archiveTime = "20060102T150405Z"

func archiveName(app string, t time.Time) string {
	return app + "-" + t.UTC().Format(archiveTime) + ".nxb"
}

func parseArchiveName(n string) (string, time.Time, bool) {
	m := archiveRe.FindStringSubmatch(n)
	if m == nil {
		return "", time.Time{}, false
	}
	t, err := time.Parse(archiveTime, m[2])
	return m[1], t, err == nil
}

// writeArchive encrypts a tar.gz with the manifest first and fill's entries after it.
func writeArchive(w io.Writer, rcpt age.Recipient, man backupManifest, fill func(*tar.Writer) error) error {
	aw, err := age.Encrypt(w, rcpt)
	if err != nil {
		return err
	}
	gz, _ := gzip.NewWriterLevel(aw, gzip.BestSpeed)
	tw := tar.NewWriter(gz)
	mb, _ := json.MarshalIndent(man, "", "  ")
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Typeflag: tar.TypeReg, Mode: 0o600, Size: int64(len(mb)), ModTime: time.Now()}); err != nil {
		return err
	}
	tw.Write(mb)
	if err := fill(tw); err != nil {
		return err
	}
	for _, c := range []io.Closer{tw, gz, aw} {
		if err := c.Close(); err != nil {
			return err
		}
	}
	return nil
}

// addTree adds dir (not following symlinks) below prefix. skip gets slash paths relative to dir.
func addTree(tw *tar.Writer, dir, prefix string, skip func(rel string) bool) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		rel = filepath.ToSlash(rel)
		if rel != "." && skip != nil && skip(rel) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		link := ""
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			link, _ = os.Readlink(p)
		case info.Mode().IsRegular(), info.IsDir():
		default:
			return nil // sockets, devices and pipes are not data
		}
		hdr, err := tar.FileInfoHeader(info, link)
		if err != nil {
			return err
		}
		hdr.Name = prefix
		if rel != "." {
			hdr.Name = prefix + "/" + rel
		}
		if info.IsDir() {
			hdr.Name += "/"
		}
		hdr.Uname, hdr.Gname = "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			f, err := os.Open(p)
			if err != nil {
				return err
			}
			_, err = io.CopyN(tw, f, hdr.Size) // a file that grew is cut at its header size
			f.Close()
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// openArchive decrypts r and unpacks it into dir (which must exist), then returns the manifest.
func openArchive(r io.Reader, id age.Identity, dir string) (backupManifest, error) {
	var man backupManifest
	ar, err := age.Decrypt(r, id)
	if err != nil {
		return man, uerr("This backup could not be opened with the backup key of this Box.")
	}
	gz, err := gzip.NewReader(ar)
	if err != nil {
		return man, uerr("This backup file is damaged.")
	}
	if err := extractTar(tar.NewReader(gz), dir); err != nil {
		return man, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil || json.Unmarshal(b, &man) != nil || man.Format != 1 || validID(man.App) != nil && man.App != "box" {
		return man, uerr("This backup file has no valid contents list.")
	}
	return man, nil
}

var archiveTops = []string{"app", "datadir", "volumes", "box"}

func allowedEntry(name string) bool {
	if name == "manifest.json" {
		return true
	}
	top, _, _ := strings.Cut(name, "/")
	for _, t := range archiveTops {
		if top == t {
			return true
		}
	}
	return false
}

var errForged = userError{"This backup file contains paths outside its own folders. It was not restored."}

// extractTar unpacks into dir. Every name must be a clean relative path below one of the known
// top folders; os.Root refuses to follow links out of dir. setuid/setgid bits are dropped.
func extractTar(tr *tar.Reader, dir string) error {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return err
	}
	defer root.Close()
	asRoot := os.Geteuid() == 0
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return uerr("This backup file is damaged.")
		}
		name := strings.TrimSuffix(hdr.Name, "/")
		if !fs.ValidPath(name) || name == "." || path.Clean(name) != name || !allowedEntry(name) {
			return errForged
		}
		mode := fs.FileMode(hdr.Mode) & 0o1777 // no setuid or setgid
		if parent := path.Dir(name); parent != "." {
			if err := root.MkdirAll(parent, 0o755); err != nil {
				return errForged
			}
		}
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := root.MkdirAll(name, 0o700); err != nil {
				return errForged
			}
			if err := root.Chmod(name, mode); err != nil {
				return errForged
			}
		case tar.TypeReg:
			f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
			if err != nil {
				return errForged
			}
			_, err = io.Copy(f, tr)
			f.Close()
			if err != nil {
				return uerr("This backup file is damaged.")
			}
			if err := root.Chmod(name, mode); err != nil {
				return errForged
			}
		case tar.TypeSymlink:
			if err := root.Symlink(hdr.Linkname, name); err != nil {
				return errForged
			}
		case tar.TypeLink:
			ln := path.Clean(hdr.Linkname)
			if !fs.ValidPath(ln) || !allowedEntry(ln) || root.Link(ln, name) != nil {
				return errForged
			}
		default:
			continue
		}
		if asRoot {
			root.Lchown(name, hdr.Uid, hdr.Gid)
		}
	}
}

// keepPolicy is a schedule's retention. All zero keeps every backup.
type keepPolicy struct {
	Last   int `json:"last"`
	Daily  int `json:"daily"`
	Weekly int `json:"weekly"`
}

// pruneList returns the archives the policy does not keep: the newest Last ones, the newest of
// each of the last Daily days and of the last Weekly ISO weeks that have backups.
func pruneList(list []archiveInfo, k keepPolicy) []string {
	if k.Last <= 0 && k.Daily <= 0 && k.Weekly <= 0 {
		return nil
	}
	sorted := append([]archiveInfo(nil), list...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].At > sorted[j].At })
	keep := map[string]bool{}
	for i := 0; i < len(sorted) && i < k.Last; i++ {
		keep[sorted[i].Name] = true
	}
	bucket := func(n int, key func(time.Time) string) {
		seen := map[string]bool{}
		for _, a := range sorted {
			if len(seen) >= n {
				return
			}
			b := key(time.Unix(a.At, 0).UTC())
			if !seen[b] {
				seen[b] = true
				keep[a.Name] = true
			}
		}
	}
	bucket(k.Daily, func(t time.Time) string { return t.Format("2006-01-02") })
	bucket(k.Weekly, func(t time.Time) string { y, w := t.ISOWeek(); return fmt.Sprintf("%d-%d", y, w) })
	var del []string
	for _, a := range sorted {
		if !keep[a.Name] {
			del = append(del, a.Name)
		}
	}
	return del
}
