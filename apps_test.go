package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/nexovirt/box/internal/catalog"
)

func TestBuiltinCatalogVerifies(t *testing.T) {
	cat, src, err := loadCatalog("")
	if err != nil || src != "built-in" {
		t.Fatalf("built-in catalog: %v %s", err, src)
	}
	if len(cat.Index.Apps) < 300 {
		t.Fatalf("only %d apps", len(cat.Index.Apps))
	}
	app, err := cat.App(cat.Index.Apps[0].ID)
	if err != nil || app.Compose == "" {
		t.Fatalf("first app: %v", err)
	}
}

func TestTamperedCatalogRefused(t *testing.T) {
	cat, _, _ := loadCatalog("")
	e := cat.Index.Apps[0]
	idx, _ := builtinCatalogFS.ReadFile("catalog/index.json")
	sig, _ := builtinCatalogFS.ReadFile("catalog/index.sig")
	file, _ := builtinCatalogFS.ReadFile("catalog/" + e.File)

	// A changed app file no longer matches the signed hash.
	evil := strings.Replace(string(file), `"compose": "`, `"compose": "services: {x: {privileged: true}}\n`, 1)
	c, err := catalog.Load(fstest.MapFS{"index.json": {Data: idx}, "index.sig": {Data: sig}, e.File: {Data: []byte(evil)}}, catalog.PublicKeys)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.App(e.ID); err == nil {
		t.Fatal("a changed app file was accepted")
	}
	// A changed index fails the signature.
	badIdx := strings.Replace(string(idx), e.SHA256, strings.Repeat("0", 64), 1)
	if _, err := catalog.Load(fstest.MapFS{"index.json": {Data: []byte(badIdx)}, "index.sig": {Data: sig}}, catalog.PublicKeys); err == nil {
		t.Fatal("a changed index was accepted")
	}
}

func TestSyncedCatalogMustNotBeOlder(t *testing.T) {
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "catalog"), 0o700)
	// An unsigned file in the synced folder is ignored.
	os.WriteFile(filepath.Join(dir, "catalog", "index.json"), []byte(`{"version":1,"generated_at":9999999999,"apps":[]}`), 0o600)
	os.WriteFile(filepath.Join(dir, "catalog", "index.sig"), []byte("AAAA"), 0o600)
	_, src, err := loadCatalog(dir)
	if err != nil || src != "built-in" {
		t.Fatalf("unsigned synced catalog used: %s %v", src, err)
	}
}

func TestBuildEnvValidates(t *testing.T) {
	app := &catalog.App{Summary: catalog.Summary{ID: "demo", Name: "Demo"}, MainPort: 8080, Questions: []catalog.Question{
		{Key: "APP_PORT", Label: "Web port", Type: "port", Default: "8080", Required: true},
		{Key: "ADMIN_USER", Label: "Username", Type: "text", Required: true, Min: 3},
		{Key: "SECRET_DB", Label: "Db", Type: "password", Generate: true, Min: 24},
		{Key: "MODE", Label: "Mode", Type: "select", Options: []string{"a", "b"}},
	}}
	cases := map[string]map[string]string{
		"required":   {},
		"short":      {"ADMIN_USER": "ab"},
		"quote":      {"ADMIN_USER": "bob's"},
		"newline":    {"ADMIN_USER": "bob\nX=1"},
		"bad port":   {"ADMIN_USER": "bob", "APP_PORT": "70000"},
		"bad select": {"ADMIN_USER": "bob", "MODE": "c"},
	}
	for name, ans := range cases {
		if _, _, err := buildEnv(app, ans, "/x/demo", "/x"); err == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	env, port, err := buildEnv(app, map[string]string{"ADMIN_USER": "bob", "APP_PORT": "9000", "MODE": "b", "APP_ID": "evil"}, "/x/demo", "/x")
	if err != nil {
		t.Fatal(err)
	}
	s := string(env)
	if port != 9000 || !strings.Contains(s, "APP_PORT='9000'") || !strings.Contains(s, "APP_ID='demo'") || !strings.Contains(s, "APP_DATA_DIR='/x/demo/data'") {
		t.Fatalf("env: %s", s)
	}
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "SECRET_DB=") && len(l) < len("SECRET_DB=''")+32 {
			t.Fatalf("secret not generated: %s", l)
		}
	}
}

func TestPublishedPorts(t *testing.T) {
	cfg := `{"services":{"web":{"ports":[{"target":80,"published":"8080","protocol":"tcp"},{"target":53,"published":"53","protocol":"udp"}]},
	"game":{"ports":[{"target":25500,"published":"25500-25502"}]},"db":{"ports":[{"target":5432}]}}}`
	got, err := publishedPorts([]byte(cfg))
	if err != nil {
		t.Fatal(err)
	}
	want := map[hostPort]bool{{"tcp", 8080}: true, {"udp", 53}: true, {"tcp", 25500}: true, {"tcp", 25501}: true, {"tcp", 25502}: true}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for _, p := range got {
		if !want[p] {
			t.Fatalf("unexpected %v", p)
		}
	}
}
