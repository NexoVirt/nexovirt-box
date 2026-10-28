package main

// Backup targets and the backup key. Both live in the helper (root): /etc/nexovirt-box/backup/
// holds key.txt (the age identity) and targets.json (with S3 and PBS secrets), 0600. The web
// process only ever sees targets with their secrets removed.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"filippo.io/age"
	"github.com/minio/minio-go/v7"
	s3creds "github.com/minio/minio-go/v7/pkg/credentials"
)

type backupTarget struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"` // local, s3, pbs
	// local
	Path string `json:"path,omitempty"`
	// s3
	Endpoint  string `json:"endpoint,omitempty"` // host[:port]
	Bucket    string `json:"bucket,omitempty"`
	Region    string `json:"region,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	TLS       bool   `json:"tls,omitempty"`
	AccessKey string `json:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty"`
	// pbs
	Repository  string `json:"repository,omitempty"` // user@realm@host:datastore
	Password    string `json:"password,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	// Secrets the browser does not get back, only whether they are set.
	Has []string `json:"has,omitempty"`
}

var (
	targetIDRe  = regexp.MustCompile(`^[a-z0-9]{8,16}$`)
	bucketRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
	prefixRe    = regexp.MustCompile(`^[A-Za-z0-9._/-]{0,100}$`)
	hostPortRe  = regexp.MustCompile(`^[A-Za-z0-9.-]+(:\d{1,5})?$|^\[[0-9a-fA-F:]+\](:\d{1,5})?$`)
	regionRe    = regexp.MustCompile(`^[a-z0-9-]{0,40}$`)
	pbsRepoRe   = regexp.MustCompile(`^[A-Za-z0-9._-]+@[A-Za-z0-9._-]+(![A-Za-z0-9._-]+)?@[A-Za-z0-9.:\[\]-]+:[A-Za-z0-9_-]+$`)
	fingerRe    = regexp.MustCompile(`^([0-9a-fA-F]{2}:){31}[0-9a-fA-F]{2}$`)
	controlChar = regexp.MustCompile(`[\x00-\x1f\x7f]`)
)

func (t backupTarget) validate() error {
	if t.Name = strings.TrimSpace(t.Name); t.Name == "" || len(t.Name) > 60 || controlChar.MatchString(t.Name) {
		return uerr("Give the target a name of up to 60 characters.")
	}
	switch t.Type {
	case "local":
		return nil // the folder is checked by the helper (checkLocalTarget)
	case "s3":
		if !hostPortRe.MatchString(t.Endpoint) {
			return uerr("Enter the S3 address as host or host:port, without https:// or a path.")
		}
		if !t.TLS && !privateHost(strings.Trim(hostOnly(t.Endpoint), "[]")) {
			return uerr("Plain HTTP is only allowed to this server or a private network. Turn on HTTPS.")
		}
		if !bucketRe.MatchString(t.Bucket) {
			return uerr("The bucket name may use lowercase letters, digits, dots and dashes.")
		}
		if !prefixRe.MatchString(t.Prefix) || strings.Contains(t.Prefix, "..") {
			return uerr("The folder in the bucket may use letters, digits, dots, dashes and slashes.")
		}
		if !regionRe.MatchString(t.Region) {
			return uerr("The region looks wrong (for example eu-central-1).")
		}
		if len(t.AccessKey) > 200 || len(t.SecretKey) > 200 || controlChar.MatchString(t.AccessKey+t.SecretKey) {
			return uerr("The access keys look wrong.")
		}
	case "pbs":
		if !pbsRepoRe.MatchString(t.Repository) {
			return uerr("Enter the repository as user@realm@host:datastore, for example backup@pbs@10.0.0.5:box.")
		}
		if t.Fingerprint != "" && !fingerRe.MatchString(t.Fingerprint) {
			return uerr("The fingerprint is 32 pairs of hex digits separated by colons.")
		}
		if len(t.Password) > 500 || controlChar.MatchString(t.Password) {
			return uerr("The password or token looks wrong.")
		}
	default:
		return uerr("Pick a target type.")
	}
	return nil
}

func hostOnly(hp string) string {
	if h, _, err := net.SplitHostPort(hp); err == nil {
		return h
	}
	return hp
}

// privateHost: loopback or a private address (literal or resolved).
func privateHost(host string) bool {
	ips := []net.IP{net.ParseIP(host)}
	if ips[0] == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		addrs, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
		if err != nil || len(addrs) == 0 {
			return false
		}
		ips = addrs
	}
	for _, ip := range ips {
		if !(ip.IsLoopback() || ip.IsPrivate()) {
			return false
		}
	}
	return true
}

func (t backupTarget) secretsSet() []string {
	var out []string
	if t.SecretKey != "" {
		out = append(out, "secret_key")
	}
	if t.Password != "" {
		out = append(out, "password")
	}
	return out
}

func (t backupTarget) redacted() backupTarget {
	t.Has = t.secretsSet()
	t.SecretKey, t.Password = "", ""
	return t
}

// checkLocalTarget accepts an absolute folder outside system folders and Box's own state
// (Box's default backups folder is the one exception inside it).
func (h *helperServer) checkLocalTarget(p string) error {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.Count(p, "/") < 2 {
		return uerr("Use a full folder path like /srv/backups.")
	}
	if p == h.defaultBackupDir() {
		return nil
	}
	if underAny(p, append(systemDirs, "/var/lib/docker", h.base(), "/etc/nexovirt-box", "/run/nexovirt-box")) || within(h.base(), p) {
		return uerr("%s is a system folder or holds Box itself. Pick a folder under /srv, /mnt, /home or /opt.", p)
	}
	return nil
}

func (h *helperServer) defaultBackupDir() string { return filepath.Join(h.base(), "backups") }

// ---- key and targets on disk ----

var backupMu sync.Mutex // guards key.txt and targets.json

func (h *helperServer) backupCfgDir() string {
	d := h.etcDir
	if d == "" {
		d = "/etc/nexovirt-box"
	}
	return filepath.Join(d, "backup")
}

func (h *helperServer) loadTargets() []backupTarget {
	var ts []backupTarget
	readJSONFile(filepath.Join(h.backupCfgDir(), "targets.json"), &ts)
	return ts
}

func (h *helperServer) saveTargets(ts []backupTarget) error {
	os.MkdirAll(h.backupCfgDir(), 0o700)
	return writeJSONFile(filepath.Join(h.backupCfgDir(), "targets.json"), ts)
}

func (h *helperServer) target(id string) (backupTarget, error) {
	for _, t := range h.loadTargets() {
		if t.ID == id {
			return t, nil
		}
	}
	return backupTarget{}, uerr("This backup target does not exist any more.")
}

func (h *helperServer) identity() (*age.X25519Identity, error) {
	b, err := os.ReadFile(filepath.Join(h.backupCfgDir(), "key.txt"))
	if err != nil {
		return nil, uerr("Set up the backup key first (Backups, Encryption key).")
	}
	return age.ParseX25519Identity(strings.TrimSpace(string(b)))
}

// backupKey: op "status" (default), "create" (returns the secret once) or "import".
func (h *helperServer) backupKey(req helperRequest, _ func(string)) (any, error) {
	backupMu.Lock()
	defer backupMu.Unlock()
	path := filepath.Join(h.backupCfgDir(), "key.txt")
	id, _ := h.identity()
	switch req.Op {
	case "", "status":
		if id == nil {
			return map[string]any{"configured": false}, nil
		}
		return map[string]any{"configured": true, "recipient": id.Recipient().String()}, nil
	case "create", "import":
		if id != nil && req.Confirm != "REPLACE" {
			return nil, uerr("A backup key is already set up.")
		}
		var nid *age.X25519Identity
		var err error
		if req.Op == "create" {
			nid, err = age.GenerateX25519Identity()
		} else if nid, err = age.ParseX25519Identity(strings.TrimSpace(req.Key)); err != nil {
			return nil, uerr("This is not a NexoVirt Box backup key. It starts with AGE-SECRET-KEY-1.")
		}
		if err != nil {
			return nil, err
		}
		os.MkdirAll(h.backupCfgDir(), 0o700)
		if err := os.WriteFile(path+".tmp", []byte(nid.String()+"\n"), 0o600); err != nil {
			return nil, err
		}
		if err := os.Rename(path+".tmp", path); err != nil {
			return nil, err
		}
		out := map[string]any{"configured": true, "recipient": nid.Recipient().String()}
		if req.Op == "create" {
			out["secret"] = nid.String() // shown once in the UI, never again
		}
		return out, nil
	}
	return nil, uerr("This action is not allowed.")
}

func (h *helperServer) backupTargets(req helperRequest, _ func(string)) (any, error) {
	out := []backupTarget{}
	for _, t := range h.loadTargets() {
		out = append(out, t.redacted())
	}
	_, err := exec.LookPath("proxmox-backup-client")
	return map[string]any{"targets": out, "pbs_client": err == nil, "default_path": h.defaultBackupDir()}, nil
}

func (h *helperServer) backupTargetSave(req helperRequest, _ func(string)) (any, error) {
	if req.Target == nil {
		return nil, uerr("The request could not be read.")
	}
	t := *req.Target
	t.Name, t.Has = strings.TrimSpace(t.Name), nil
	if err := t.validate(); err != nil {
		return nil, err
	}
	if t.Type == "local" {
		if err := h.checkLocalTarget(t.Path); err != nil {
			return nil, err
		}
	}
	backupMu.Lock()
	defer backupMu.Unlock()
	ts := h.loadTargets()
	if t.ID == "" {
		t.ID = randomToken()[:12]
		ts = append(ts, t)
	} else {
		if !targetIDRe.MatchString(t.ID) {
			return nil, uerr("This backup target does not exist any more.")
		}
		found := false
		for i, old := range ts {
			if old.ID == t.ID {
				if t.SecretKey == "" {
					t.SecretKey = old.SecretKey // left empty in the form: keep it
				}
				if t.Password == "" {
					t.Password = old.Password
				}
				ts[i], found = t, true
			}
		}
		if !found {
			return nil, uerr("This backup target does not exist any more.")
		}
	}
	if t.Type == "local" {
		if err := os.MkdirAll(t.Path, 0o700); err != nil {
			return nil, uerr("The folder %s could not be created.", t.Path)
		}
	}
	if err := h.saveTargets(ts); err != nil {
		return nil, err
	}
	return t.redacted(), nil
}

func (h *helperServer) backupTargetDelete(req helperRequest, _ func(string)) (any, error) {
	backupMu.Lock()
	defer backupMu.Unlock()
	ts := h.loadTargets()
	out := ts[:0]
	for _, t := range ts {
		if t.ID != req.TargetID {
			out = append(out, t)
		}
	}
	return map[string]bool{"ok": true}, h.saveTargets(out) // backups already on the target stay
}

// ---- stores ----

type backupStore interface {
	put(ctx context.Context, name, file string) error
	list(ctx context.Context) ([]archiveInfo, error)
	get(ctx context.Context, name, file string) error
	del(ctx context.Context, name string) error
}

func (h *helperServer) openStore(t backupTarget) (backupStore, error) {
	switch t.Type {
	case "local":
		if err := h.checkLocalTarget(t.Path); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(t.Path, 0o700); err != nil {
			return nil, uerr("The backup folder %s could not be created.", t.Path)
		}
		return localStore{t.Path}, nil
	case "s3":
		c, err := minio.New(t.Endpoint, &minio.Options{Creds: s3creds.NewStaticV4(t.AccessKey, t.SecretKey, ""), Secure: t.TLS, Region: t.Region})
		if err != nil {
			return nil, uerr("The S3 address is not valid.")
		}
		p := strings.Trim(t.Prefix, "/")
		if p != "" {
			p += "/"
		}
		return s3Store{c, t.Bucket, p}, nil
	case "pbs":
		if _, err := exec.LookPath("proxmox-backup-client"); err != nil {
			return nil, uerr("Proxmox Backup Server targets need proxmox-backup-client on this server. Install it from the Proxmox repository (package proxmox-backup-client), then try again.")
		}
		return pbsStore{t}, nil
	}
	return nil, uerr("Pick a target type.")
}

type localStore struct{ dir string }

func (s localStore) root() (*os.Root, error) {
	r, err := os.OpenRoot(s.dir)
	if err != nil {
		return nil, uerr("The backup folder %s could not be opened.", s.dir)
	}
	return r, nil
}

func (s localStore) put(_ context.Context, name, file string) error {
	r, err := s.root()
	if err != nil {
		return err
	}
	defer r.Close()
	src, err := os.Open(file)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := r.OpenFile(name+".part", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return uerr("The backup could not be written to %s.", s.dir)
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		r.Remove(name + ".part")
		return diskErr(err, s.dir)
	}
	if err := dst.Close(); err != nil {
		return diskErr(err, s.dir)
	}
	return r.Rename(name+".part", name)
}

func diskErr(err error, where string) error {
	if strings.Contains(err.Error(), "no space") {
		return uerr("%s is full. Free some space or pick another target.", where)
	}
	return err
}

func (s localStore) list(context.Context) ([]archiveInfo, error) {
	es, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, uerr("The backup folder %s could not be read.", s.dir)
	}
	out := []archiveInfo{}
	for _, e := range es {
		app, at, ok := parseArchiveName(e.Name())
		if !ok || !e.Type().IsRegular() {
			continue
		}
		info, _ := e.Info()
		out = append(out, archiveInfo{Name: e.Name(), App: app, At: at.Unix(), Size: info.Size()})
	}
	return out, nil
}

func (s localStore) get(_ context.Context, name, file string) error {
	r, err := s.root()
	if err != nil {
		return err
	}
	defer r.Close()
	src, err := r.Open(name)
	if err != nil {
		return uerr("This backup is not in %s any more.", s.dir)
	}
	defer src.Close()
	return copyToFile(src, file)
}

func (s localStore) del(_ context.Context, name string) error {
	r, err := s.root()
	if err != nil {
		return err
	}
	defer r.Close()
	if err := r.Remove(name); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func copyToFile(src io.Reader, file string) error {
	dst, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return diskErr(err, "This server's disk")
	}
	return dst.Close()
}

type s3Store struct {
	c      *minio.Client
	bucket string
	prefix string
}

func s3Err(err error) error {
	var er minio.ErrorResponse
	if errors.As(err, &er) {
		switch er.Code {
		case "NoSuchBucket":
			return uerr("The bucket does not exist. Create it first.")
		case "AccessDenied", "InvalidAccessKeyId", "SignatureDoesNotMatch":
			return uerr("The S3 storage refused the access keys.")
		}
		return uerr("The S3 storage reported: %s", er.Message)
	}
	return uerr("The S3 storage could not be reached. Check the address and the network.")
}

func (s s3Store) put(ctx context.Context, name, file string) error {
	if _, err := s.c.FPutObject(ctx, s.bucket, s.prefix+name, file, minio.PutObjectOptions{ContentType: "application/octet-stream"}); err != nil {
		return s3Err(err)
	}
	return nil
}

func (s s3Store) list(ctx context.Context) ([]archiveInfo, error) {
	out := []archiveInfo{}
	for o := range s.c.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: s.prefix}) {
		if o.Err != nil {
			return nil, s3Err(o.Err)
		}
		n := strings.TrimPrefix(o.Key, s.prefix)
		if app, at, ok := parseArchiveName(n); ok {
			out = append(out, archiveInfo{Name: n, App: app, At: at.Unix(), Size: o.Size})
		}
	}
	return out, nil
}

func (s s3Store) get(ctx context.Context, name, file string) error {
	if err := s.c.FGetObject(ctx, s.bucket, s.prefix+name, file, minio.GetObjectOptions{}); err != nil {
		return s3Err(err)
	}
	return nil
}

func (s s3Store) del(ctx context.Context, name string) error {
	if err := s.c.RemoveObject(ctx, s.bucket, s.prefix+name, minio.RemoveObjectOptions{}); err != nil {
		return s3Err(err)
	}
	return nil
}

// pbsStore keeps each archive as a host backup "nxbox-<app>" with one image file in it. The
// archive is already encrypted by Box; PBS adds its own deduplication.
type pbsStore struct{ t backupTarget }

func (s pbsStore) run(ctx context.Context, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "proxmox-backup-client", append(args, "--repository", s.t.Repository)...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "HOME=/root", "PBS_PASSWORD=" + s.t.Password}
	if s.t.Fingerprint != "" {
		cmd.Env = append(cmd.Env, "PBS_FINGERPRINT="+s.t.Fingerprint)
	}
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, uerr("Proxmox Backup Server reported: %s", strings.TrimSpace(lastLine(string(out))))
	}
	return out, nil
}

func (s pbsStore) put(ctx context.Context, name, file string) error {
	app, at, _ := parseArchiveName(name)
	_, err := s.run(ctx, "backup", "archive.img:"+file, "--backup-type", "host", "--backup-id", "nxbox-"+app,
		"--backup-time", fmt.Sprint(at.Unix()))
	return err
}

func (s pbsStore) list(ctx context.Context) ([]archiveInfo, error) {
	out, err := s.run(ctx, "snapshot", "list", "--output-format", "json")
	if err != nil {
		return nil, err
	}
	var snaps []struct {
		Type string `json:"backup-type"`
		ID   string `json:"backup-id"`
		Time int64  `json:"backup-time"`
		Size int64  `json:"size"`
	}
	json.Unmarshal(out, &snaps)
	res := []archiveInfo{}
	for _, sn := range snaps {
		app, ok := strings.CutPrefix(sn.ID, "nxbox-")
		if sn.Type != "host" || !ok || (validID(app) != nil && app != "box") {
			continue
		}
		res = append(res, archiveInfo{Name: archiveName(app, time.Unix(sn.Time, 0)), App: app, At: sn.Time, Size: sn.Size})
	}
	return res, nil
}

func pbsSnapshot(name string) string {
	app, at, _ := parseArchiveName(name)
	return "host/nxbox-" + app + "/" + at.UTC().Format("2006-01-02T15:04:05Z")
}

func (s pbsStore) get(ctx context.Context, name, file string) error {
	_, err := s.run(ctx, "restore", pbsSnapshot(name), "archive.img", file)
	return err
}

func (s pbsStore) del(ctx context.Context, name string) error {
	_, err := s.run(ctx, "snapshot", "forget", pbsSnapshot(name))
	return err
}

// ---- list, delete, prune ----

func (h *helperServer) storeFor(id string) (backupStore, error) {
	t, err := h.target(id)
	if err != nil {
		return nil, err
	}
	return h.openStore(t)
}

func (h *helperServer) backupList(req helperRequest, _ func(string)) (any, error) {
	st, err := h.storeFor(req.TargetID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	list, err := st.list(ctx)
	if err != nil {
		return nil, err
	}
	out := []archiveInfo{}
	for _, a := range list {
		if req.ID == "" || a.App == req.ID {
			out = append(out, a)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].At > out[j].At })
	return out, nil
}

func (h *helperServer) backupDelete(req helperRequest, _ func(string)) (any, error) {
	if _, _, ok := parseArchiveName(req.Name); !ok {
		return nil, uerr("This backup does not exist.")
	}
	st, err := h.storeFor(req.TargetID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	return map[string]bool{"ok": true}, st.del(ctx, req.Name)
}

// backupPrune deletes the backups of one app that the schedule's retention does not keep.
func (h *helperServer) backupPrune(req helperRequest, logf func(string)) (any, error) {
	if validID(req.ID) != nil && req.ID != "box" {
		return nil, uerr("This app does not exist.")
	}
	st, err := h.storeFor(req.TargetID)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	list, err := st.list(ctx)
	if err != nil {
		return nil, err
	}
	mine := []archiveInfo{}
	for _, a := range list {
		if a.App == req.ID {
			mine = append(mine, a)
		}
	}
	deleted := []string{}
	for _, n := range pruneList(mine, req.Keep) {
		if err := st.del(ctx, n); err != nil {
			return nil, err
		}
		deleted = append(deleted, n)
	}
	return map[string]any{"deleted": deleted}, nil
}
