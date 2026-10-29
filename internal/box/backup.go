package box

// Backups in the helper. A backup of an app is consistent because the app is stopped while its
// files are read (`docker compose stop`, then `start` again; a few seconds to minutes of
// downtime) and its named volumes are read straight from Docker's volume folders. The archive
// is written to a temporary file first, then the app runs again and the file is uploaded.

import (
	"archive/tar"
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"filippo.io/age"
)

var volKeyRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,63}$`)

func (h *helperServer) backupTmp() (string, error) {
	d := filepath.Join(h.base(), "backup-tmp")
	return d, os.MkdirAll(d, 0o700)
}

type composeVolume struct{ Name, Key, Mount string }

// appVolumes lists the named volumes Docker Compose created for the project.
func appVolumes(ctx context.Context, id string) []composeVolume {
	out, err := exec.CommandContext(ctx, "docker", "volume", "ls", "--filter", "label=com.docker.compose.project=nxbox-"+id,
		"--format", `{{.Name}}|{{.Label "com.docker.compose.volume"}}`).Output()
	if err != nil {
		return nil
	}
	var vols []composeVolume
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name, key, ok := strings.Cut(l, "|")
		if !ok || !volKeyRe.MatchString(key) {
			continue
		}
		mp, err := exec.CommandContext(ctx, "docker", "volume", "inspect", "--format", "{{.Mountpoint}}", name).Output()
		mount := strings.TrimSpace(string(mp))
		if err != nil || !strings.HasPrefix(mount, "/var/lib/docker/volumes/") {
			continue // only local volumes in Docker's own folder
		}
		vols = append(vols, composeVolume{Name: name, Key: key, Mount: mount})
	}
	return vols
}

func (h *helperServer) backupRun(ctx context.Context, req helperRequest, logf func(string)) (any, error) {
	if validID(req.ID) != nil && req.ID != "box" {
		return nil, uerr("This app does not exist.")
	}
	t, err := h.target(req.TargetID)
	if err != nil {
		return nil, err
	}
	st, err := h.openStore(t)
	if err != nil {
		return nil, err
	}
	id, err := h.identity()
	if err != nil {
		return nil, err
	}
	tmp, err := h.backupTmp()
	if err != nil {
		return nil, err
	}
	start := time.Now()
	name := archiveName(req.ID, start)
	file := filepath.Join(tmp, name+".part")
	defer os.Remove(file)
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nil, err
	}
	if req.ID == "box" {
		err = h.writeBoxArchive(f, id.Recipient(), logf)
	} else {
		err = h.writeAppArchive(ctx, f, req.ID, id.Recipient(), logf)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return nil, diskErr(err, "This server's disk")
	}
	info, _ := os.Stat(file)
	logf(fmt.Sprintf("Uploading %s (%s) to %s", name, humanBytes(info.Size()), t.Name))
	ctx, cancel := context.WithTimeout(ctx, 6*time.Hour)
	defer cancel()
	if err := st.put(ctx, name, file); err != nil {
		return nil, err
	}
	logf("Backup saved.")
	return map[string]any{"name": name, "size": info.Size(), "seconds": int(time.Since(start).Seconds()), "target": t.ID}, nil
}

func humanBytes(n int64) string {
	units := []string{"B", "KB", "MB", "GB", "TB"}
	f, i := float64(n), 0
	for f >= 1024 && i < len(units)-1 {
		f /= 1024
		i++
	}
	return fmt.Sprintf("%.1f %s", f, units[i])
}

// writeAppArchive stops the app if it runs, archives its folders and volumes and starts it again.
func (h *helperServer) writeAppArchive(ctx context.Context, w io.Writer, appID string, rcpt age.Recipient, logf func(string)) error {
	release, err := h.claim(appID, "backup")
	if err != nil {
		return err
	}
	defer release()
	meta, err := h.readMeta(appID)
	if err != nil {
		return err
	}
	dir := h.appDir(appID)
	running := false
	if needDocker() == nil {
		_, state, _ := composeStatus(dir, appID)
		running = state == "running" || state == "partial" || state == "restarting"
	}
	if running {
		logf("Stopping " + meta.Name + " so its files are consistent")
		if err := runStream(ctx, compose(dir, appID, "stop"), logf); err != nil {
			return err
		}
		defer func() {
			logf("Starting " + meta.Name + " again")
			sctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			if err := runStream(sctx, compose(dir, appID, "start"), logf); err != nil {
				log.Printf("backup: start %s again: %v", appID, err)
				logf("Warning: " + meta.Name + " could not be started again. Start it in My Apps.")
			}
		}()
	}
	var vols []composeVolume
	if needDocker() == nil {
		vols = appVolumes(ctx, appID)
	}
	man := backupManifest{Format: 1, App: appID, Name: meta.Name, Created: time.Now().Unix(), BoxVersion: version, Custom: meta.Custom}
	outside := meta.DataDir != "" && meta.DataDir != filepath.Join(dir, "data") && !within(meta.DataDir, dir)
	if outside {
		man.DataDir = meta.DataDir
	}
	for _, v := range vols {
		man.Volumes = append(man.Volumes, v.Key)
	}
	logf("Packing and encrypting " + meta.Name)
	return writeArchive(w, rcpt, man, func(tw *tar.Writer) error {
		if err := addTree(tw, dir, "app", func(rel string) bool { return rel == installMarker || strings.HasSuffix(rel, ".prev") }); err != nil {
			return err
		}
		if outside && exists(meta.DataDir) {
			if err := addTree(tw, meta.DataDir, "datadir", nil); err != nil {
				return err
			}
		}
		for _, v := range vols {
			logf("Adding the volume " + v.Key)
			if err := addTree(tw, v.Mount, "volumes/"+v.Key, nil); err != nil {
				return err
			}
		}
		return nil
	})
}

// writeBoxArchive saves Box's own settings: a consistent copy of the web database and the key
// that encrypts notification secrets.
func (h *helperServer) writeBoxArchive(w io.Writer, rcpt age.Recipient, logf func(string)) error {
	tmp, err := h.backupTmp()
	if err != nil {
		return err
	}
	stage, err := os.MkdirTemp(tmp, "box-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	db, err := h.webDB()
	if err != nil {
		return err
	}
	_, err = db.Exec(`VACUUM INTO ?`, filepath.Join(stage, "box.db"))
	db.Close()
	if err != nil {
		return fmt.Errorf("copy database: %w", err)
	}
	if b, err := os.ReadFile(filepath.Join(h.webState, "notify.key")); err == nil {
		os.WriteFile(filepath.Join(stage, "notify.key"), b, 0o600)
	}
	logf("Packing and encrypting Box's settings")
	man := backupManifest{Format: 1, App: "box", Name: "NexoVirt Box settings", Created: time.Now().Unix(), BoxVersion: version}
	return writeArchive(w, rcpt, man, func(tw *tar.Writer) error { return addTree(tw, stage, "box", nil) })
}

// fetchArchive downloads and unpacks an archive into a new folder below parent.
func (h *helperServer) fetchArchive(ctx context.Context, req helperRequest, parent string, logf func(string)) (backupManifest, string, error) {
	var man backupManifest
	app, _, ok := parseArchiveName(req.Name)
	if !ok {
		return man, "", uerr("This backup does not exist.")
	}
	st, err := h.storeFor(req.TargetID)
	if err != nil {
		return man, "", err
	}
	id, err := h.identity()
	if err != nil {
		return man, "", err
	}
	tmp, err := h.backupTmp()
	if err != nil {
		return man, "", err
	}
	file := filepath.Join(tmp, req.Name+".dl")
	defer os.Remove(file)
	logf("Downloading " + req.Name)
	if err := st.get(ctx, req.Name, file); err != nil {
		return man, "", err
	}
	os.MkdirAll(parent, 0o700)
	stage, err := os.MkdirTemp(parent, ".restore-")
	if err != nil {
		return man, "", err
	}
	f, err := os.Open(file)
	if err != nil {
		os.RemoveAll(stage)
		return man, "", err
	}
	defer f.Close()
	logf("Decrypting and unpacking")
	man, err = openArchive(f, id, stage)
	if err == nil && man.App != app {
		err = uerr("This backup file does not belong to %s.", app)
	}
	if err != nil {
		os.RemoveAll(stage)
		return man, "", err
	}
	return man, stage, nil
}

// backupVerify downloads an archive, decrypts it and reads every file.
func (h *helperServer) backupVerify(ctx context.Context, req helperRequest, logf func(string)) (any, error) {
	tmp, err := h.backupTmp()
	if err != nil {
		return nil, err
	}
	man, stage, err := h.fetchArchive(ctx, req, tmp, logf)
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	files, size := 0, int64(0)
	filepath.WalkDir(stage, func(_ string, d os.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			files++
			if i, e := d.Info(); e == nil {
				size += i.Size()
			}
		}
		return nil
	})
	want := []string{"app/compose.yaml", "app/app.json"}
	if man.App == "box" {
		want = []string{"box/box.db"}
	}
	for _, v := range man.Volumes {
		want = append(want, "volumes/"+v)
	}
	for _, p := range want {
		if !exists(filepath.Join(stage, p)) {
			return nil, uerr("The backup opened, but %s is missing in it.", p)
		}
	}
	logf(fmt.Sprintf("The backup is complete: %d files, %s.", files, humanBytes(size)))
	return map[string]any{"ok": true, "files": files, "bytes": size, "app": man.App, "created": man.Created}, nil
}
