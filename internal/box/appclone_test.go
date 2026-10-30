package box

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloneYAMLAndPorts(t *testing.T) {
	cfg := []byte(`{"services":{"web":{"container_name":"wiki","ports":[{"target":80,"published":"8080","protocol":"tcp"},{"host_ip":"127.0.0.1","target":9000,"published":"9000"}]},"db":{"image":"pg"}}}`)
	plan, err := planClonePorts([]hostPort{{"tcp", 8080}, {"tcp", 9000}}, nil, func(p int) bool { return p == 8080 || p == 8081 })
	if err != nil || plan[8080] != 8082 || plan[9000] != 9000 {
		t.Fatalf("plan %v %v", plan, err)
	}
	b, err := cloneYAML(cfg, "wiki-2", plan, false)
	if err != nil {
		t.Fatal(err)
	}
	want := "  web:\n    container_name: nxbox-wiki-2-web\n    ports: !override\n      - \"8082:80/tcp\"\n      - \"127.0.0.1:9000:9000/tcp\"\n"
	if !strings.HasSuffix(string(b), want) || strings.Contains(string(b), "db:") {
		t.Fatalf("got\n%s", b)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, cloneFile), b, 0o600)
	if err := checkCloneFile(dir); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, cloneFile), []byte("services:\n  web:\n    container_name: evil\n"), 0o600)
	if checkCloneFile(dir) == nil {
		t.Fatal("foreign container name accepted")
	}
	if _, err := cloneYAML([]byte(`{"services":{"w":{"ports":[{"target":1,"published":"10-20"}]}}}`), "x", nil, false); err == nil {
		t.Fatal("port range accepted")
	}
	// The wanted main port wins over the automatic choice.
	if plan, _ := planClonePorts([]hostPort{{"tcp", 8080}}, map[int]int{8080: 7000}, func(int) bool { return true }); plan[8080] != 7000 {
		t.Fatalf("wanted port ignored: %v", plan)
	}
}

func TestCloneRefusesSharedFolders(t *testing.T) {
	dir, shared := "/var/lib/nexovirt-box/apps/wiki-2", "/var/lib/nexovirt-box/shared"
	ok := `{"services":{"w":{"volumes":[{"type":"bind","source":"` + dir + `/data"},{"type":"bind","source":"` + shared + `/media"},{"type":"volume","source":"db"}]}}}`
	if err := sharedFolders([]byte(ok), dir, shared); err != nil {
		t.Fatal(err)
	}
	bad := `{"services":{"w":{"volumes":[{"type":"bind","source":"/srv/wiki"}]}}}`
	if err := sharedFolders([]byte(bad), dir, shared); err == nil || !strings.Contains(err.Error(), "/srv/wiki") {
		t.Fatalf("shared host folder accepted: %v", err)
	}
}

// End to end with a fake Docker: the clone gets its own folder, id, data dir, container names and
// a free port; the original stays untouched.
func TestAppClone(t *testing.T) {
	bin := t.TempDir()
	script := `#!/bin/sh
case "$*" in
  *"config --format json"*) echo '{"services":{"web":{"image":"x","container_name":"wiki","ports":[{"target":80,"published":"8080","protocol":"tcp"}]}}}' ;;
esac
`
	os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	h := &helperServer{appsDir: t.TempDir(), webState: t.TempDir(), proofs: newProofKeeper()}
	src := h.appDir("wiki")
	os.MkdirAll(src, 0o700)
	os.WriteFile(filepath.Join(src, "compose.yaml"), []byte("services:\n  web:\n    image: x\n    container_name: wiki\n    ports: ['8080:80']\n"), 0o600)
	os.WriteFile(filepath.Join(src, ".env"), []byte("APP_ID='wiki'\nAPP_DATA_DIR='"+filepath.Join(src, "data")+"'\nPASSWORD='s'\n"), 0o600)
	h.writeMeta(installedApp{ID: "wiki", Name: "Wiki", MainPort: 8080, Domain: "wiki.example.com", Protect: true, DomainPort: 8080})
	res, err := h.appClone(t.Context(), helperRequest{ID: "wiki", NewID: "wiki-2", Name: "Wiki 2"}, func(string) {})
	if err != nil {
		t.Fatal(err)
	}
	m := res.(installedApp)
	if m.ID != "wiki-2" || m.Domain != "" || m.Protect || m.CloneOf != "wiki" || m.DataDir != filepath.Join(h.appDir("wiki-2"), "data") {
		t.Fatalf("clone meta %+v", m)
	}
	env, _ := os.ReadFile(filepath.Join(h.appDir("wiki-2"), ".env"))
	if !strings.Contains(string(env), "APP_ID='wiki-2'") || !strings.Contains(string(env), "wiki-2/data") || !strings.Contains(string(env), "PASSWORD='s'") {
		t.Fatalf(".env %s", env)
	}
	cy, _ := os.ReadFile(filepath.Join(h.appDir("wiki-2"), cloneFile))
	if !strings.Contains(string(cy), "container_name: nxbox-wiki-2-web") {
		t.Fatalf("clone.yaml %s", cy)
	}
	if orig, _ := h.readMeta("wiki"); orig.Name != "Wiki" || !orig.Protect {
		t.Fatal("original changed")
	}
	if _, err := h.appClone(t.Context(), helperRequest{ID: "wiki", NewID: "wiki-2", Name: "Again"}, func(string) {}); err == nil {
		t.Fatal("second clone with the same id accepted")
	}
}

// A protected source gives a loopback-only clone; fixed or external volume names never point the
// clone at the original's data.
func TestCloneKeepsProtectionAndScopesVolumes(t *testing.T) {
	cfg := []byte(`{"services":{"web":{"ports":[{"target":80,"published":"8080"}]}},
		"volumes":{"db":{"name":"wiki_db"},"cache":{"name":"nxbox-wiki-2_cache"}},
		"networks":{"default":{"name":"nxbox-wiki-2_default"},"proxy":{"name":"proxy","external":true},"back":{"name":"wiki_back"}}}`)
	b, err := cloneYAML(cfg, "wiki-2", map[int]int{8080: 8081}, true)
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{`"127.0.0.1:8081:80/tcp"`, "volumes:\n  db:\n    name: nxbox-wiki-2_db\n", "networks:\n  back:\n    name: nxbox-wiki-2_back\n"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in\n%s", want, s)
		}
	}
	if strings.Contains(s, "cache:") || strings.Contains(s, "proxy") {
		t.Fatalf("already scoped or external entries rewritten:\n%s", s)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, cloneFile), b, 0o600)
	if err := checkCloneFile(dir); err != nil {
		t.Fatalf("own file refused: %v\n%s", err, s)
	}
	os.WriteFile(filepath.Join(dir, cloneFile), []byte("volumes:\n  db:\n    name: wiki_db\n"), 0o600)
	if checkCloneFile(dir) == nil {
		t.Fatal("a volume name outside the clone's project accepted")
	}
	if _, err := cloneYAML([]byte(`{"services":{},"volumes":{"data":{"name":"shared","external":true}}}`), "wiki-2", nil, false); err == nil {
		t.Fatal("external volume accepted")
	}
}

// A failed clone removes only volumes of its own project, never the original's.
func TestCloneCleanupLeavesSourceVolumes(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(bin, "log")
	script := `#!/bin/sh
echo "$*" >> ` + log + `
case "$*" in
  *"volume ls"*) printf 'nxbox-wiki-2_db|db\nwiki_db|db\n' ;;
  *"volume inspect"*) echo /var/lib/docker/volumes/x/_data ;;
esac
`
	os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	cleanupClone("wiki-2", t.TempDir())
	calls, _ := os.ReadFile(log)
	c := string(calls)
	if strings.Contains(c, "--volumes") || strings.Contains(c, "volume rm wiki_db") || !strings.Contains(c, "volume rm nxbox-wiki-2_db") {
		t.Fatalf("cleanup calls:\n%s", c)
	}
}
