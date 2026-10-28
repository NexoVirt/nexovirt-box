package main

// Restores. A restore replaces an app's files and volumes with the ones in the archive: the app
// is stopped, the current folders are moved aside, the archive's folders take their place, the
// result goes through the same checks as an install (folders, privileges, ports) and the app
// starts again. Any failure puts the previous folders back. Every restore needs a fresh
// password proof that the helper checks itself, because the archive decides what runs.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

var errProofRestore = userError{"Enter your password to restore a backup."}

func (h *helperServer) backupRestore(ctx context.Context, req helperRequest, logf func(string)) (any, error) {
	app, _, ok := parseArchiveName(req.Name)
	if !ok {
		return nil, uerr("This backup does not exist.")
	}
	if !h.consoleAllowed(req) {
		return nil, errProofRestore
	}
	if app == "box" {
		return h.restoreBox(ctx, req, logf)
	}
	newID := req.NewID
	if newID == "" {
		newID = app
	}
	if validID(newID) != nil || newID == "box" {
		return nil, uerr("Use lowercase letters, digits and dashes for the app name.")
	}
	if err := needDocker(); err != nil {
		return nil, err
	}
	release, err := h.claim(newID, "restore")
	if err != nil {
		return nil, err
	}
	defer release()
	dir := h.appDir(newID)
	installed := exists(filepath.Join(dir, "app.json"))
	if newID != app && exists(dir) {
		return nil, uerr("An app called %s already exists. Pick another name.", newID)
	}
	ctx, cancel := context.WithTimeout(ctx, 6*time.Hour)
	defer cancel()
	man, stage, err := h.fetchArchive(ctx, req, h.appsDir, logf)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	for _, v := range man.Volumes {
		if !volKeyRe.MatchString(v) {
			return nil, errForged
		}
	}
	staged := filepath.Join(stage, "app")
	if !exists(filepath.Join(staged, "compose.yaml")) {
		return nil, uerr("This backup has no app definition in it.")
	}

	var meta installedApp
	if installed {
		// An installed app keeps its current compose file and settings; data and .env come back.
		if meta, err = h.readMeta(newID); err != nil {
			return nil, err
		}
		for _, f := range []string{"compose.yaml", "app.json"} {
			if err := copyPrivate(filepath.Join(dir, f), filepath.Join(staged, f)); err != nil {
				return nil, err
			}
		}
	} else {
		b, _ := os.ReadFile(filepath.Join(staged, "app.json"))
		if json.Unmarshal(b, &meta) != nil {
			return nil, uerr("This backup has no valid app settings in it.")
		}
		meta.ID, meta.InstalledAt, meta.Stopped = newID, time.Now().Unix(), false
		meta.Domain, meta.TLS, meta.DataCreated = "", "", false // domains are not taken over
		meta.SocketOK = req.AllowDockerSocket
		if newID != app {
			meta.Name += " (restored)"
		}
		if checkOwnName(meta.Name) != nil {
			meta.Name = newID
		}
	}

	dataDir, stagedData := filepath.Join(dir, "data"), filepath.Join(stage, "datadir")
	external := false
	if man.DataDir != "" && exists(stagedData) {
		switch {
		case installed && meta.DataDir == man.DataDir:
			external = true
		case !installed && newID == app && checkDataDir(man.DataDir) == nil:
			external, meta.DataCreated = true, !exists(man.DataDir)
		default: // the old folder is taken: the data goes into the app's own folder
			os.RemoveAll(filepath.Join(staged, "data"))
			if err := os.Rename(stagedData, filepath.Join(staged, "data")); err != nil {
				return nil, err
			}
		}
		if external {
			dataDir = man.DataDir
		}
	}
	if !installed {
		meta.DataDir = dataDir
		mb, _ := json.MarshalIndent(meta, "", "  ")
		if err := os.WriteFile(filepath.Join(staged, "app.json"), mb, 0o600); err != nil {
			return nil, err
		}
		if err := rewriteEnvKeys(filepath.Join(staged, ".env"), map[string]string{"APP_ID": newID, "APP_DATA_DIR": dataDir}); err != nil {
			return nil, err
		}
	}

	if installed {
		logf("Stopping " + meta.Name)
		if err := runStream(ctx, compose(dir, newID, "stop"), logf); err != nil {
			return nil, err
		}
	}
	stamp := time.Now().Format("20060102150405")
	var undo []func()
	rollback := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
		if installed {
			logf("The previous files are back. Starting " + meta.Name + " again.")
			if !meta.Stopped {
				runStream(context.Background(), compose(dir, newID, "up", "-d"), logf)
			}
		}
	}
	// 1. the app folder
	old := dir + ".pre-restore-" + stamp // not a valid app id, so My Apps never lists it
	if exists(dir) {
		if err := os.Rename(dir, old); err != nil {
			return nil, err
		}
	}
	if err := os.Rename(staged, dir); err != nil {
		os.Rename(old, dir)
		return nil, err
	}
	undo = append(undo, func() {
		os.RemoveAll(dir)
		if exists(old) {
			os.Rename(old, dir)
		}
	})
	// 2. a data folder outside the app folder
	if external {
		oldData := dataDir + ".pre-restore-" + stamp
		if exists(dataDir) {
			if err := os.Rename(dataDir, oldData); err != nil {
				rollback()
				return nil, err
			}
		}
		undo = append(undo, func() {
			os.RemoveAll(dataDir)
			if exists(oldData) {
				os.Rename(oldData, dataDir)
			}
		})
		if err := moveDir(stagedData, dataDir); err != nil {
			rollback()
			return nil, err
		}
	}
	// 3. checks, as for an install
	logf("Checking ports and folders")
	if err := checkConfig(ctx, dir, newID, h.policy(meta), !installed); err != nil {
		rollback()
		return nil, err
	}
	// 4. volumes
	if len(man.Volumes) > 0 {
		if err := runStream(ctx, compose(dir, newID, "up", "--no-start"), logf); err != nil {
			rollback()
			return nil, err
		}
		mounts := map[string]string{}
		for _, v := range appVolumes(ctx, newID) {
			mounts[v.Key] = v.Mount
		}
		for _, key := range man.Volumes {
			mount, ok := mounts[key]
			if !ok {
				logf("The volume " + key + " is no longer used by the app. It was skipped.")
				continue
			}
			logf("Restoring the volume " + key)
			aside := mount + ".pre-restore-" + stamp
			if err := os.Rename(mount, aside); err != nil {
				rollback()
				return nil, err
			}
			undo = append(undo, func() { os.RemoveAll(mount); os.Rename(aside, mount) })
			if err := moveDir(filepath.Join(stage, "volumes", key), mount); err != nil {
				rollback()
				return nil, err
			}
		}
	}
	// 5. start
	if !meta.Stopped {
		logf("Starting " + meta.Name)
		if err := runStream(ctx, compose(dir, newID, "up", "-d", "--remove-orphans"), logf); err != nil {
			rollback()
			return nil, err
		}
	}
	os.Remove(filepath.Join(dir, installMarker))
	for _, p := range []string{old, dataDir + ".pre-restore-" + stamp} {
		os.RemoveAll(p)
	}
	for _, v := range appVolumes(context.Background(), newID) {
		os.RemoveAll(v.Mount + ".pre-restore-" + stamp)
	}
	logf(meta.Name + " was restored from " + req.Name + ".")
	return map[string]any{"app": newID, "name": meta.Name}, nil
}

// moveDir moves src to dst (which must not exist), copying when they are on different disks.
func moveDir(src, dst string) error {
	err := os.Rename(src, dst)
	if err == nil || !errors.Is(err, syscall.EXDEV) {
		return err
	}
	if err := os.MkdirAll(dst, 0o755); err != nil {
		return err
	}
	if out, err := exec.Command("cp", "-a", "--", src+"/.", dst).CombinedOutput(); err != nil {
		return uerr("The files could not be copied: %s", strings.TrimSpace(lastLine(string(out))))
	}
	return os.RemoveAll(src)
}

// rewriteEnvKeys points Box's own variables at the restored app's place (only keys that exist).
func rewriteEnvKeys(path string, want map[string]string) error {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	lines := strings.Split(string(b), "\n")
	for i, l := range lines {
		k, _, ok := strings.Cut(l, "=")
		if w, mine := want[strings.TrimSpace(k)]; ok && mine {
			lines[i] = strings.TrimSpace(k) + "='" + w + "'"
		}
	}
	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600)
}

// restoreBox puts Box's settings back. The web service is stopped for it, so this answers
// first and does the swap a moment later; everyone signs in again afterwards.
func (h *helperServer) restoreBox(ctx context.Context, req helperRequest, logf func(string)) (any, error) {
	tmp, err := h.backupTmp()
	if err != nil {
		return nil, err
	}
	_, stage, err := h.fetchArchive(ctx, req, tmp, logf)
	if err != nil {
		return nil, err
	}
	db := filepath.Join(stage, "box", "box.db")
	if st, err := os.Lstat(db); err != nil || !st.Mode().IsRegular() {
		os.RemoveAll(stage)
		return nil, uerr("This backup has no Box database in it.")
	}
	logf("Box restarts in a moment with the restored settings. Sign in again afterwards.")
	go func() {
		defer os.RemoveAll(stage)
		time.Sleep(2 * time.Second)
		exec.Command("systemctl", "stop", "nexovirt-box").Run()
		for _, f := range []string{"box.db", "notify.key"} {
			src := filepath.Join(stage, "box", f)
			if st, err := os.Lstat(src); err != nil || !st.Mode().IsRegular() {
				continue
			}
			dst := filepath.Join(h.webState, f)
			if err := copyPrivate(src, dst+".restore"); err != nil {
				log.Printf("restore box: %v", err)
				continue
			}
			os.Chmod(dst+".restore", 0o600)
			os.Lchown(dst+".restore", h.allowUID, -1)
			os.Rename(dst+".restore", dst)
		}
		os.Remove(filepath.Join(h.webState, "box.db-wal")) // belongs to the old database
		os.Remove(filepath.Join(h.webState, "box.db-shm"))
		exec.Command("systemctl", "start", "nexovirt-box").Run()
	}()
	return map[string]any{"app": "box", "restarting": true}, nil
}

// copyPrivate copies a small file with mode 0600.
func copyPrivate(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o600)
}
