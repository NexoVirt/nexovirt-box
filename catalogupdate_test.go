package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nexovirt/box/internal/catalog"
)

func TestBase64QuestionIsMinRandomBytes(t *testing.T) {
	q := catalog.Question{Key: "K", Type: "text", Generate: true, Format: "base64", Min: 32, Max: 10}
	a, b := generateValue(q), generateValue(q)
	raw, err := base64.StdEncoding.DecodeString(a)
	if err != nil || len(raw) != 32 || a == b {
		t.Fatalf("got %q (%d bytes, err %v)", a, len(raw), err)
	}
	app := &catalog.App{MainPort: 1111, Questions: []catalog.Question{q}}
	env, _, err := buildEnv(app, nil, t.TempDir(), t.TempDir(), t.TempDir())
	if err != nil { // a generated value is never refused by Max
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(env), "\n") {
		if v, ok := strings.CutPrefix(l, "K='"); ok {
			if raw, err := base64.StdEncoding.DecodeString(strings.TrimSuffix(v, "'")); err != nil || len(raw) != 32 {
				t.Fatalf("env line %q", l)
			}
			return
		}
	}
	t.Fatal("K missing from .env")
}

func TestMergeEnvNeverOverwrites(t *testing.T) {
	env := []byte("# Written by NexoVirt Box.\nADMIN_PASSWORD='keepme'\nAPP_PORT='8080'\n")
	app := &catalog.App{Questions: []catalog.Question{
		{Key: "ADMIN_PASSWORD", Type: "password", Generate: true},
		{Key: "SECRET_AUTH_PASSWORD", Type: "password"},
		{Key: "MODE", Type: "text", Default: "prod"},
	}}
	out, added, err := mergeEnv(env, app)
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "ADMIN_PASSWORD='keepme'\n") || strings.Count(s, "ADMIN_PASSWORD=") != 1 {
		t.Fatalf("existing answer changed:\n%s", s)
	}
	if strings.Join(added, ",") != "SECRET_AUTH_PASSWORD,MODE" || !strings.Contains(s, "MODE='prod'") || strings.Contains(s, "SECRET_AUTH_PASSWORD=''") {
		t.Fatalf("added %v:\n%s", added, s)
	}
	if again, added2, _ := mergeEnv(out, app); string(again) != s || len(added2) != 0 {
		t.Fatalf("second merge changed the file: %v", added2)
	}
	if _, _, err := mergeEnv(env, &catalog.App{Questions: []catalog.Question{{Key: "NEW", Label: "New", Required: true}}}); err == nil {
		t.Fatal("a new required question without a default must be refused")
	}
}

func TestCatalogUpdateAvailable(t *testing.T) {
	dir := t.TempDir()
	app := &catalog.App{Compose: "services: {a: {image: x:2}}\n"}
	os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services: {a: {image: x:1}}\n"), 0o600)
	if !catalogUpdateAvailable(installedApp{}, dir, app) {
		t.Fatal("old install without a recorded sha: the file on disk differs")
	}
	if catalogUpdateAvailable(installedApp{ComposeSHA: composeSHA([]byte(app.Compose))}, dir, app) {
		t.Fatal("recorded sha matches the catalog")
	}
	if catalogUpdateAvailable(installedApp{Custom: true}, dir, app) {
		t.Fatal("own apps never get catalog updates")
	}
}

func TestSocketGate(t *testing.T) {
	app := &catalog.App{}
	app.DockerSocket = true
	m := installedApp{}
	if _, err := socketGate(&m, app, false, func() bool { return true }); err == nil || !strings.HasPrefix(err.Error(), "needs_docker_socket:") {
		t.Fatalf("not asked: %v", err)
	}
	if _, err := socketGate(&m, app, true, func() bool { return false }); err == nil || m.SocketOK {
		t.Fatal("allowed without a password proof")
	}
	if changed, err := socketGate(&m, app, true, func() bool { return true }); err != nil || !changed || !m.SocketOK {
		t.Fatalf("not stored: %v", err)
	}
	if changed, err := socketGate(&m, app, false, func() bool { return false }); err != nil || changed {
		t.Fatal("asked again after it was allowed")
	}
	if _, err := socketGate(&installedApp{}, &catalog.App{}, false, nil); err != nil {
		t.Fatal("apps without the socket are never asked")
	}
}
