package main

import (
	"archive/tar"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
)

func tarOf(t *testing.T, entries []tar.Header, bodies map[string]string) *tar.Reader {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, h := range entries {
		h := h
		b := bodies[h.Name]
		h.Size = int64(len(b))
		if h.Typeflag != tar.TypeReg {
			h.Size = 0
		}
		if h.Mode == 0 {
			h.Mode = 0o644
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Size > 0 {
			tw.Write([]byte(b))
		}
	}
	tw.Close()
	return tar.NewReader(&buf)
}

// A forged archive must never write outside the restore folder.
func TestExtractStaysInside(t *testing.T) {
	outside := t.TempDir()
	cases := map[string][]tar.Header{
		"dotdot":   {{Name: "app/../../escape", Typeflag: tar.TypeReg}},
		"absolute": {{Name: "/" + filepath.Join(outside, "abs"), Typeflag: tar.TypeReg}},
		"unknown":  {{Name: "etc/passwd", Typeflag: tar.TypeReg}},
		"via-link": {
			{Name: "app/", Typeflag: tar.TypeDir, Mode: 0o755},
			{Name: "app/l", Typeflag: tar.TypeSymlink, Linkname: outside},
			{Name: "app/l/owned", Typeflag: tar.TypeReg},
		},
		"hardlink-out": {
			{Name: "app/h", Typeflag: tar.TypeLink, Linkname: "../../" + filepath.Join(outside, "x")},
		},
	}
	for name, entries := range cases {
		stage := t.TempDir()
		bodies := map[string]string{}
		for _, e := range entries {
			bodies[e.Name] = "pwned"
		}
		err := extractTar(tarOf(t, entries, bodies), stage)
		if err == nil {
			t.Errorf("%s: forged archive accepted", name)
		}
		if es, _ := os.ReadDir(outside); len(es) > 0 {
			t.Fatalf("%s: wrote outside: %v", name, es)
		}
	}
}

func TestExtractKeepsFilesAndStripsSetuid(t *testing.T) {
	stage := t.TempDir()
	entries := []tar.Header{
		{Name: "manifest.json", Typeflag: tar.TypeReg},
		{Name: "app/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "app/data/db.sqlite", Typeflag: tar.TypeReg, Mode: 0o4755},
		{Name: "app/data/link", Typeflag: tar.TypeSymlink, Linkname: "db.sqlite"},
		{Name: "volumes/pg/PG_VERSION", Typeflag: tar.TypeReg},
	}
	err := extractTar(tarOf(t, entries, map[string]string{"manifest.json": "{}", "app/data/db.sqlite": "data", "volumes/pg/PG_VERSION": "16"}), stage)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(stage, "app/data/db.sqlite"))
	if err != nil || st.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		t.Fatalf("setuid kept or file missing: %v %v", st, err)
	}
	if l, _ := os.Readlink(filepath.Join(stage, "app/data/link")); l != "db.sqlite" {
		t.Fatalf("symlink not kept: %q", l)
	}
}

func TestArchiveRoundTrip(t *testing.T) {
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, "data"), 0o755)
	os.WriteFile(filepath.Join(src, "compose.yaml"), []byte("services: {}\n"), 0o600)
	os.WriteFile(filepath.Join(src, "data", "kuma.db"), []byte("hello"), 0o644)
	os.WriteFile(filepath.Join(src, installMarker), nil, 0o600)
	id, _ := age.GenerateX25519Identity()
	var buf bytes.Buffer
	man := backupManifest{Format: 1, App: "uptime-kuma", Created: 1}
	if err := writeArchive(&buf, id.Recipient(), man, func(tw *tar.Writer) error {
		return addTree(tw, src, "app", func(rel string) bool { return rel == installMarker })
	}); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(buf.Bytes(), []byte("hello")) {
		t.Fatal("archive is not encrypted")
	}
	stage := t.TempDir()
	got, err := openArchive(bytes.NewReader(buf.Bytes()), id, stage)
	if err != nil || got.App != "uptime-kuma" {
		t.Fatalf("open: %v %+v", err, got)
	}
	if b, _ := os.ReadFile(filepath.Join(stage, "app/data/kuma.db")); string(b) != "hello" {
		t.Fatalf("data lost: %q", b)
	}
	if exists(filepath.Join(stage, "app", installMarker)) {
		t.Fatal("skipped file was archived")
	}
	other, _ := age.GenerateX25519Identity()
	if _, err := openArchive(bytes.NewReader(buf.Bytes()), other, t.TempDir()); err == nil {
		t.Fatal("wrong key opened the archive")
	}
}

func TestArchiveNames(t *testing.T) {
	ts := time.Date(2026, 10, 28, 9, 5, 0, 0, time.UTC)
	n := archiveName("uptime-kuma", ts)
	app, at, ok := parseArchiveName(n)
	if !ok || app != "uptime-kuma" || !at.Equal(ts) {
		t.Fatalf("%s -> %s %v %v", n, app, at, ok)
	}
	for _, bad := range []string{"../x-20261028T090500Z.nxb", "x-20261028T090500Z.nxb/../../etc", "X-20261028T090500Z.nxb", "x.nxb", "x-20261028T090500Z.nxb.tmp"} {
		if _, _, ok := parseArchiveName(bad); ok {
			t.Errorf("bad name accepted: %s", bad)
		}
	}
}

func TestRetention(t *testing.T) {
	day := func(d, h int) archiveInfo {
		at := time.Date(2026, 10, d, h, 0, 0, 0, time.UTC)
		return archiveInfo{Name: archiveName("a", at), At: at.Unix()}
	}
	list := []archiveInfo{day(28, 3), day(28, 1), day(27, 3), day(26, 3), day(20, 3), day(13, 3), day(1, 3)}
	if del := pruneList(list, keepPolicy{}); len(del) != 0 {
		t.Fatalf("no policy must keep everything: %v", del)
	}
	del := pruneList(list, keepPolicy{Last: 1, Daily: 3, Weekly: 2})
	keep := map[string]bool{}
	for _, a := range list {
		keep[a.Name] = true
	}
	for _, d := range del {
		delete(keep, d)
	}
	// Newest (28 03:00) counts for last and the 28th, plus the 27th, 26th; weekly adds the 20th.
	want := []string{day(28, 3).Name, day(27, 3).Name, day(26, 3).Name, day(20, 3).Name}
	if len(keep) != len(want) {
		t.Fatalf("kept %v", keep)
	}
	for _, w := range want {
		if !keep[w] {
			t.Fatalf("%s should be kept; kept %v", w, keep)
		}
	}
}

func TestLocalTargetFolders(t *testing.T) {
	h := &helperServer{appsDir: "/var/lib/nexovirt-box/apps"}
	for _, ok := range []string{"/var/lib/nexovirt-box/backups", "/srv/backups", "/mnt/usb/box"} {
		if err := h.checkLocalTarget(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"/", "/etc", "/etc/cron.d", "/var/lib/nexovirt-box/apps", "/var/lib/nexovirt-box/web",
		"/var/lib/nexovirt-box", "/etc/nexovirt-box/backup", "relative/dir", "/srv/../etc", "/root/x", "/usr/local/bin"} {
		if err := h.checkLocalTarget(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestS3TargetValidation(t *testing.T) {
	good := backupTarget{Type: "s3", Name: "s3", Endpoint: "s3.eu-central-1.amazonaws.com", Bucket: "my-box", TLS: true, AccessKey: "a", SecretKey: "b"}
	if err := good.validate(); err != nil {
		t.Fatal(err)
	}
	plainLocal := good
	plainLocal.Endpoint, plainLocal.TLS = "127.0.0.1:9000", false
	if err := plainLocal.validate(); err != nil {
		t.Fatalf("plain http to this machine refused: %v", err)
	}
	for _, mut := range []func(*backupTarget){
		func(b *backupTarget) { b.Endpoint = "https://s3.example.com" },
		func(b *backupTarget) { b.Endpoint = "s3.example.com/path" },
		func(b *backupTarget) { b.Endpoint, b.TLS = "s3.example.com", false },
		func(b *backupTarget) { b.Bucket = "../x" },
		func(b *backupTarget) { b.Prefix = "../up" },
	} {
		b := good
		mut(&b)
		if err := b.validate(); err == nil {
			t.Errorf("accepted %+v", b)
		}
	}
	if s := strings.Join(good.redacted().Has, ","); s != "secret_key" {
		t.Fatalf("redaction: %s", s)
	}
	if good.redacted().SecretKey != "" {
		t.Fatal("secret returned")
	}
}

func TestScheduleDue(t *testing.T) {
	loc := time.UTC
	now := time.Date(2026, 10, 28, 10, 0, 0, 0, loc) // a Wednesday
	d := backupSchedule{Freq: "daily", Time: "03:30"}.dueAt(now)
	if !d.Equal(time.Date(2026, 10, 28, 3, 30, 0, 0, loc)) {
		t.Fatalf("daily: %v", d)
	}
	d = backupSchedule{Freq: "daily", Time: "23:00"}.dueAt(now)
	if !d.Equal(time.Date(2026, 10, 27, 23, 0, 0, 0, loc)) {
		t.Fatalf("daily later today: %v", d)
	}
	d = backupSchedule{Freq: "weekly", Weekday: 0, Time: "02:00"}.dueAt(now)
	if !d.Equal(time.Date(2026, 10, 25, 2, 0, 0, 0, loc)) {
		t.Fatalf("weekly: %v", d)
	}
}
