package main

import (
	"os"
	"strings"
	"testing"

	"github.com/nexovirt/box/internal/catalog"
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
	got := firstLoginCreds(app, map[string]string{}, nil, func() []string { return []string{"x", cleanLogLine(alistLine)} })
	if len(got) != 2 || got[0].Value != "admin" || got[1].Value != "2qGnVpOM" || got[1].Source != "log" || !got[1].Secret {
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
		if _, err := app.FirstLogin.LogRegexp(); err != nil {
			t.Errorf("%s: %v", e.ID, err)
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
	got := firstLoginCreds(app, map[string]string{"PW": "pw1"}, have, func() []string { called = true; return nil })
	if len(got) != 2 || got[1].Value != "admin" || called {
		t.Fatalf("got %+v called %v", got, called)
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
