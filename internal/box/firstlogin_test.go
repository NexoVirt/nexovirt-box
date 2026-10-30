package box

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NexoVirt/nexovirt-box/internal/catalog"
)

// The real AList start line (colour codes included, as docker prints them).
const alistLine = "\x1b[36mINFO\x1b[0m[2026-10-28 17:54:39] Successfully created the admin user and the initial password is: 2qGnVpOM "

func TestAListFirstLoginFromCatalog(t *testing.T) {
	cat, _, err := loadCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	app, err := cat.App("alist")
	if err != nil {
		t.Fatal(err)
	}
	got, pending := firstLoginCreds(app, map[string]string{}, nil, firstLoginSources{logs: func() []string { return []string{"x", cleanLogLine(alistLine)} }})
	if pending || len(got) != 2 || got[0].Value != "admin" || got[1].Value != "2qGnVpOM" || got[1].Source != "log" || !got[1].Secret {
		t.Fatalf("got %+v", got)
	}
}

func TestEveryCatalogFirstLoginIsValid(t *testing.T) {
	cat, _, err := loadCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range cat.Index.Apps {
		app, err := cat.App(e.ID)
		if err != nil {
			t.Fatal(err)
		}
		if app.FirstLogin == nil {
			continue
		}
		n++
		fl := app.FirstLogin
		if _, err := fl.LogRegexp(); err != nil {
			t.Errorf("%s: %v", e.ID, err)
		}
		if _, err := fl.FileRegexp(); err != nil {
			t.Errorf("%s: %v", e.ID, err)
		}
		if _, err := fl.FilePath(); err != nil {
			t.Errorf("%s: %v", e.ID, err)
		}
		if fl.PasswordFromFileRegex != "" && fl.PasswordFromFile == "" {
			t.Errorf("%s: a file pattern without a file", e.ID)
		}
		if fl.Username == "" && fl.Password == "" && fl.PasswordFromEnv == "" && fl.PasswordFromFile == "" && fl.PasswordFromLog == "" && fl.Note == "" {
			t.Errorf("%s: empty first_login", e.ID)
		}
		if v := app.FirstLogin.PasswordFromEnv; v != "" && !strings.Contains(app.Compose, v) {
			t.Errorf("%s: %s is not used by the compose file", e.ID, v)
		}
	}
	if n == 0 {
		t.Fatal("no app has first_login")
	}
}

func TestGenericLogSecrets(t *testing.T) {
	lines := []string{
		cleanLogLine(alistLine),
		"Generated admin password: s3cr3t-Pass",
		"default credentials: admin / hunter22",
		"The admin password is not set, using the token",
		"initial password: ********",
		"Initial password is: 2qGnVpOM", // duplicate
		"admin token=abcd1234efgh",
	}
	got := genericLogSecrets(lines)
	want := []string{"2qGnVpOM", "s3cr3t-Pass", "hunter22"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}
	if v := genericLogSecrets([]string{"listening on :8080", "user admin logged in"}); len(v) != 0 {
		t.Fatalf("false positive %v", v)
	}
}

func TestFirstLoginSkipsKnownValues(t *testing.T) {
	app := &catalog.App{FirstLogin: &catalog.FirstLogin{Username: "admin", PasswordFromEnv: "PW"}}
	have := []appCred{{Label: "Admin password", Value: "pw1", Secret: true}}
	called := false
	got, pending := firstLoginCreds(app, map[string]string{"PW": "pw1"}, have, firstLoginSources{logs: func() []string { called = true; return nil }})
	if len(got) != 2 || got[0].Value != "admin" || got[1].Value != "pw1" || called || pending {
		t.Fatalf("got %+v called %v pending %v", got, called, pending)
	}
}

// The file Crafty 4 writes on its first start (main.py, json.dumps indent=4).
const craftyCreds = `{
    "username": "admin",
    "password": "k3Jx9_Qp2LmZ",
    "info": "This is NOT where you change your password. This file is only a means to give you a default password."
}`

func TestCraftyFirstLoginFromFile(t *testing.T) {
	cat, _, err := loadCatalog("")
	if err != nil {
		t.Fatal(err)
	}
	app, err := cat.App("crafty")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	rel, err := app.FirstLogin.FilePath()
	if err != nil || rel == "" {
		t.Fatalf("crafty file path %q %v", rel, err)
	}
	read := firstLoginSources{logs: func() []string { return nil }, file: func(r string) ([]byte, error) { return readDataFile(dir, r) }}
	got, pending := firstLoginCreds(app, nil, nil, read)
	if !pending || len(got) != 1 || got[0].Value != "admin" {
		t.Fatalf("before the first start: %+v pending %v", got, pending)
	}
	os.MkdirAll(filepath.Join(dir, filepath.Dir(rel)), 0o700)
	os.WriteFile(filepath.Join(dir, rel), []byte(craftyCreds), 0o600)
	got, pending = firstLoginCreds(app, nil, nil, read)
	if pending || len(got) != 2 || got[1].Value != "k3Jx9_Qp2LmZ" || got[1].Source != "file" || !got[1].Secret {
		t.Fatalf("got %+v pending %v", got, pending)
	}
}

func TestReadDataFileStaysInside(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	secret := filepath.Join(outside, "shadow")
	os.WriteFile(secret, []byte("root:secret"), 0o600)
	os.WriteFile(filepath.Join(dir, "ok.txt"), []byte(" pw \n"), 0o600)
	os.Symlink(secret, filepath.Join(dir, "link"))
	os.Symlink(outside, filepath.Join(dir, "dirlink"))
	os.Mkdir(filepath.Join(dir, "sub"), 0o700)
	os.WriteFile(filepath.Join(dir, "big"), make([]byte, maxCredFile+1), 0o600)
	if b, err := readDataFile(dir, "ok.txt"); err != nil || fileSecret(b, nil) != "pw" {
		t.Fatalf("ok.txt: %q %v", b, err)
	}
	for _, rel := range []string{"link", "dirlink/shadow", "../" + filepath.Base(outside) + "/shadow", "sub", "big", "missing"} {
		if b, err := readDataFile(dir, rel); err == nil {
			t.Errorf("%s was read: %q", rel, b)
		}
	}
}

func TestFirstLoginFilePathRules(t *testing.T) {
	for p, ok := range map[string]bool{
		"data/config/default-creds.txt": true, "admin.pw": true,
		"/etc/shadow": false, "../x": false, "a/../../x": false, "a//b": false, "./a": false, "a/": false, "a b": false,
		"a/b/c/d/e/f/g/h/i": false,
	} {
		_, err := (&catalog.FirstLogin{PasswordFromFile: p}).FilePath()
		if (err == nil) != ok {
			t.Errorf("%q: err %v", p, err)
		}
	}
}

func TestFirstLoginDefaultPasswordAndOrder(t *testing.T) {
	app := &catalog.App{FirstLogin: &catalog.FirstLogin{Username: "admin", Password: "admin"}}
	have := []appCred{{Label: "Database password", Value: "dbpw", Secret: true}}
	got, pending := firstLoginCreds(app, nil, have, firstLoginSources{logs: func() []string { t.Fatal("logs read"); return nil }})
	if pending || len(got) != 3 || got[0].Label != "User name" || got[1].Source != "default" || got[2].Value != "dbpw" {
		t.Fatalf("got %+v pending %v", got, pending)
	}
}

// Harness for real logs: NXBOX_LOGSCAN_FILE=alist.log go test -run TestLogScanFile -v
func TestLogScanFile(t *testing.T) {
	path := os.Getenv("NXBOX_LOGSCAN_FILE")
	if path == "" {
		t.Skip("set NXBOX_LOGSCAN_FILE")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		if len(lines) == firstLogLines {
			break
		}
		lines = append(lines, cleanLogLine(l))
	}
	cat, _, _ := loadCatalog("")
	app, _ := cat.App("alist")
	re, _ := app.FirstLogin.LogRegexp()
	t.Logf("catalog pattern: %q", scanLog(lines, re))
	t.Logf("generic scan:    %q", genericLogSecrets(lines))
}

func TestParseAndMergeLogs(t *testing.T) {
	a := parseLogOutput([]byte("2026-10-28T17:54:39.5Z \x1b[36mINFO\x1b[0m start\n2026-10-28T17:54:41Z ready\n\nwarning without time\n"), "app")
	b := parseLogOutput([]byte("2026-10-28T17:54:40.25Z db up\n"), "db")
	if len(a) != 3 || a[0].Msg != "INFO start" || a[2].Time != "" {
		t.Fatalf("parsed %+v", a)
	}
	m := mergeLogs([][]logLine{a, b}, 3)
	if len(m) != 3 || m[0].Service != "db" || m[1].Msg != "ready" || m[2].Msg != "warning without time" {
		t.Fatalf("merged %+v", m)
	}
}
