package box

// App updates with rollback. Before an update the helper stops the app, copies its folder, an
// outside data folder and its named volumes into <state>/rollback/<id>/ (one copy per app, the
// last update only) and tags the images it runs as nxbox-rollback/<id>:<n> so a prune cannot
// remove them. Then it pulls, recreates and waits for the app to be healthy. When the app does
// not come up, the old images get their tags back and the copied files replace the new ones.
// The same copy serves the manual "Roll back" of the last update.

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// updateRecord is stored in app.json: the result of the last update and whether its copy exists.
type updateRecord struct {
	At       int64  `json:"at"`
	Result   string `json:"result"`             // ok, rolled_back, failed, manual_rollback
	Rollback bool   `json:"rollback,omitempty"` // a copy of the state before it is kept
}

// rollbackInfo sits next to the copy: what to put back where.
type rollbackInfo struct {
	At      int64             `json:"at"`
	Images  map[string]string `json:"images"`             // image reference -> image id before the update
	DataDir string            `json:"data_dir,omitempty"` // data folder outside the app folder
	Volumes map[string]string `json:"volumes,omitempty"`  // volume key -> mountpoint
}

const rollbackMargin = 512 << 20 // free space left after the copy

var (
	healthWait  = 90 * time.Second
	healthEvery = 5 * time.Second
)

func (h *helperServer) rollbackDir(id string) string { return filepath.Join(h.base(), "rollback", id) }

// treeSize adds up regular files below dir (symlinks are not followed).
func treeSize(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if i, err := d.Info(); err == nil {
				n += i.Size()
			}
		}
		return nil
	})
	return n
}

func freeBytes(dir string) int64 {
	var st unix.Statfs_t
	if unix.Statfs(dir, &st) != nil {
		return -1
	}
	return int64(st.Bavail) * int64(st.Bsize) //nolint:unconvert // field types differ per OS
}

// copyTree copies src to dst (which must not exist) keeping modes, owners, times and symlinks
// as links. Sockets, devices and pipes are skipped.
func copyTree(src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		to := filepath.Join(dst, rel)
		info, err := os.Lstat(p)
		if err != nil {
			return err
		}
		switch {
		case d.IsDir():
			if err := os.Mkdir(to, info.Mode().Perm()); err != nil {
				return err
			}
		case d.Type()&fs.ModeSymlink != 0:
			t, err := os.Readlink(p)
			if err != nil {
				return err
			}
			if err := os.Symlink(t, to); err != nil {
				return err
			}
		case d.Type().IsRegular():
			if err := copyTreeFile(p, to, info.Mode().Perm()); err != nil {
				return err
			}
		default:
			return nil
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			os.Lchown(to, int(st.Uid), int(st.Gid))
		}
		if d.Type()&fs.ModeSymlink == 0 {
			os.Chmod(to, info.Mode()&(fs.ModePerm|fs.ModeSetgid|fs.ModeSticky)) // chown clears setgid
			os.Chtimes(to, info.ModTime(), info.ModTime())
		}
		return nil
	})
}

// putBack replaces target with a copy of snap; on failure the old target comes back.
func putBack(snap, target string) error {
	old := target + ".rollback-old"
	os.RemoveAll(old)
	had := exists(target)
	if had {
		if err := os.Rename(target, old); err != nil {
			return err
		}
	}
	if err := copyTree(snap, target); err != nil {
		os.RemoveAll(target)
		if had {
			os.Rename(old, target)
		}
		return err
	}
	return os.RemoveAll(old)
}

// imageIDs maps every image reference of the app to the image it runs now.
func imageIDs(ctx context.Context, dir, id string) map[string]string {
	cmd := compose(dir, id, "config", "--images")
	out, err := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...).Output()
	if err != nil {
		return nil
	}
	m := map[string]string{}
	for _, ref := range strings.Fields(string(out)) {
		b, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", ref).Output()
		if err == nil && strings.HasPrefix(strings.TrimSpace(string(b)), "sha256:") {
			m[ref] = strings.TrimSpace(string(b))
		}
	}
	return m
}

// snapshot copies the app's state into its rollback folder. The app must be stopped.
func (h *helperServer) snapshot(ctx context.Context, meta installedApp, logf func(string)) (rollbackInfo, error) {
	dir := h.appDir(meta.ID)
	info := rollbackInfo{At: time.Now().Unix(), Images: imageIDs(ctx, dir, meta.ID), Volumes: map[string]string{}}
	if meta.DataDir != "" && meta.DataDir != filepath.Join(dir, "data") && !within(meta.DataDir, dir) && exists(meta.DataDir) {
		info.DataDir = meta.DataDir
	}
	for _, v := range appVolumes(ctx, meta.ID) {
		info.Volumes[v.Key] = v.Mount
	}
	need := treeSize(dir)
	if info.DataDir != "" {
		need += treeSize(info.DataDir)
	}
	for _, m := range info.Volumes {
		need += treeSize(m)
	}
	rb := h.rollbackDir(meta.ID)
	os.RemoveAll(rb)
	if err := os.MkdirAll(rb, 0o700); err != nil {
		return info, err
	}
	if free := freeBytes(rb); free >= 0 && free < need+rollbackMargin {
		os.RemoveAll(rb)
		return info, uerr("Not enough free space for the rollback copy of %s (needs %s, %s free). Free up space or update without a rollback copy.",
			meta.Name, humanBytes(need), humanBytes(free))
	}
	logf(fmt.Sprintf("Saving a rollback copy of %s (%s)", meta.Name, humanBytes(need)))
	fail := func(err error) (rollbackInfo, error) { os.RemoveAll(rb); return info, err }
	if err := copyTree(dir, filepath.Join(rb, "app")); err != nil {
		return fail(diskErr(err, "This server's disk"))
	}
	if info.DataDir != "" {
		if err := copyTree(info.DataDir, filepath.Join(rb, "datadir")); err != nil {
			return fail(diskErr(err, "This server's disk"))
		}
	}
	for k, m := range info.Volumes {
		if err := copyTree(m, filepath.Join(rb, "volumes", k)); err != nil {
			return fail(diskErr(err, "This server's disk"))
		}
	}
	// Old rollback tags go first; then the current images get one so a prune keeps them.
	untagRollback(ctx, meta.ID)
	n := 0
	for _, img := range info.Images {
		exec.CommandContext(ctx, "docker", "tag", img, "nxbox-rollback/"+meta.ID+":"+strconv.Itoa(n)).Run()
		n++
	}
	b, _ := json.Marshal(info)
	if err := os.WriteFile(filepath.Join(rb, "rollback.json"), b, 0o600); err != nil {
		return fail(err)
	}
	return info, nil
}

func untagRollback(ctx context.Context, id string) {
	if out, err := exec.CommandContext(ctx, "docker", "image", "ls", "--format", "{{.Repository}}:{{.Tag}}", "nxbox-rollback/"+id).Output(); err == nil {
		for _, t := range strings.Fields(string(out)) {
			exec.CommandContext(ctx, "docker", "image", "rm", t).Run()
		}
	}
}

// dropRollback removes an app's rollback copy and image tags (uninstall).
func (h *helperServer) dropRollback(id string) {
	os.RemoveAll(h.rollbackDir(id))
	if needDocker() == nil {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		untagRollback(ctx, id)
	}
}

// restoreSnapshot puts the copied images and files back. The app is stopped first.
func (h *helperServer) restoreSnapshot(ctx context.Context, id string, logf func(string)) error {
	rb := h.rollbackDir(id)
	var info rollbackInfo
	if !readJSONFile(filepath.Join(rb, "rollback.json"), &info) {
		return uerr("There is no rollback copy for this app.")
	}
	dir := h.appDir(id)
	runStream(ctx, compose(dir, id, "stop"), logf)
	logf("Putting the previous images back")
	for ref, img := range info.Images {
		if err := exec.CommandContext(ctx, "docker", "tag", img, ref).Run(); err != nil {
			return uerr("The previous image of %s is gone, so Box cannot roll back.", ref)
		}
	}
	logf("Putting the previous files back")
	if err := putBack(filepath.Join(rb, "app"), dir); err != nil {
		return err
	}
	if info.DataDir != "" {
		if err := putBack(filepath.Join(rb, "datadir"), info.DataDir); err != nil {
			return err
		}
	}
	for k, m := range info.Volumes {
		if !volKeyRe.MatchString(k) || !strings.HasPrefix(m, "/var/lib/docker/volumes/") {
			continue
		}
		if err := putBack(filepath.Join(rb, "volumes", k), m); err != nil {
			return err
		}
	}
	return nil
}

// appHealthy waits until every container runs (twice in a row) and the main port answers.
func appHealthy(ctx context.Context, dir, id string, port int) bool {
	deadline := time.Now().Add(healthWait)
	good, portOK := 0, port == 0
	for time.Now().Before(deadline) {
		_, state, _ := composeStatus(dir, id)
		if state == "running" {
			good++
		} else {
			good = 0
		}
		if !portOK {
			if c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), 2*time.Second); err == nil {
				c.Close()
				portOK = true
			}
		}
		if good >= 2 && portOK {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(healthEvery):
		}
	}
	return false
}

// updateApp pulls and recreates with a rollback copy (unless noCopy) and rolls back on failure.
func (h *helperServer) updateApp(ctx context.Context, meta installedApp, noCopy bool, up func() error, logf func(string)) error {
	dir := h.appDir(meta.ID)
	if !noCopy {
		logf("Stopping " + meta.Name + " for the rollback copy")
		if err := runStream(ctx, compose(dir, meta.ID, "stop"), logf); err != nil {
			return err
		}
		if _, err := h.snapshot(ctx, meta, logf); err != nil {
			if !meta.Stopped {
				runStream(context.Background(), compose(dir, meta.ID, "start"), logf)
			}
			return err
		}
	} else {
		h.dropRollback(meta.ID)
	}
	rec := updateRecord{At: time.Now().Unix(), Result: "ok", Rollback: !noCopy}
	logf("Downloading the newest images of " + meta.Name)
	err := runStream(ctx, compose(dir, meta.ID, "pull"), logf)
	if err == nil {
		err = up()
	}
	if err == nil && !meta.Stopped {
		logf("Waiting for " + meta.Name + " to answer")
		if !appHealthy(ctx, dir, meta.ID, meta.MainPort) {
			err = uerr("%s did not come up after the update.", meta.Name)
		}
	}
	if err == nil {
		h.saveUpdateRecord(meta.ID, rec)
		return nil
	}
	if noCopy {
		rec.Result = "failed"
		h.saveUpdateRecord(meta.ID, rec)
		return err
	}
	logf("The update failed (" + friendlyHelperErr("app_action", err) + "). Going back to the previous version.")
	rctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if rerr := h.restoreSnapshot(rctx, meta.ID, logf); rerr != nil {
		rec.Result = "failed"
		h.saveUpdateRecord(meta.ID, rec)
		return uerr("The update failed and the rollback failed too: %s", friendlyHelperErr("app_action", rerr))
	}
	if !meta.Stopped {
		runStream(rctx, compose(dir, meta.ID, "up", "-d", "--remove-orphans"), logf)
	}
	rec.Result = "rolled_back"
	h.saveUpdateRecord(meta.ID, rec)
	return uerr("The update did not work, so Box went back to the previous version of %s.", meta.Name)
}

// rollbackApp is the manual "Roll back" of the last update.
func (h *helperServer) rollbackApp(ctx context.Context, meta installedApp, logf func(string)) error {
	if err := h.restoreSnapshot(ctx, meta.ID, logf); err != nil {
		return err
	}
	dir := h.appDir(meta.ID)
	if !meta.Stopped {
		if err := runStream(ctx, compose(dir, meta.ID, "up", "-d", "--remove-orphans"), logf); err != nil {
			return err
		}
	}
	h.saveUpdateRecord(meta.ID, updateRecord{At: time.Now().Unix(), Result: "manual_rollback", Rollback: true})
	logf(meta.Name + " is back on the version before the last update.")
	return nil
}

// saveUpdateRecord re-reads app.json (a rollback may have replaced it) and stores the result.
func (h *helperServer) saveUpdateRecord(id string, rec updateRecord) {
	m, err := h.readMeta(id)
	if err != nil {
		return
	}
	m.LastUpdate = &rec
	h.writeMeta(m)
}
