package box

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBindYAMLLoopbackOnlyForTheProtectedPort(t *testing.T) {
	cfg := []byte(`{"services":{"web":{"ports":[{"host_ip":"","target":80,"published":"8080","protocol":"tcp"},{"target":443,"published":"8443","protocol":"tcp"}]},"db":{"ports":[{"target":5432,"published":"5432"}]}}}`)
	b, ok, err := bindYAML(cfg, 8080)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	want := "services:\n  web:\n    ports: !override\n      - \"127.0.0.1:8080:80/tcp\"\n      - \"0.0.0.0:8443:443/tcp\"\n"
	if !strings.HasSuffix(string(b), want) || strings.Contains(string(b), "db:") {
		t.Fatalf("got\n%s", b)
	}
	if _, ok, _ := bindYAML(cfg, 9999); ok {
		t.Fatal("a port nobody publishes was accepted")
	}
	ranged := []byte(`{"services":{"web":{"ports":[{"target":80,"published":"8080"},{"target":9000,"published":"9000-9010"}]}}}`)
	if _, ok, _ := bindYAML(ranged, 8080); ok {
		t.Fatal("a service with a port range must not be rewritten")
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, bindFile), b, 0o600)
	if err := checkBindFile(dir); err != nil {
		t.Fatalf("own file refused: %v", err)
	}
	for _, bad := range []string{"services:\n  web:\n    privileged: true\n", "services:\n  web:\n    ports: !override\n      - \"/var/run/docker.sock:/x\"\n"} {
		os.WriteFile(filepath.Join(dir, bindFile), []byte(bad), 0o600)
		if checkBindFile(dir) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

// Turning the sign-in on binds the port to loopback (through the install checks), turning it off
// gives the normal binding back.
func TestApplyBindingProtectAndUnprotect(t *testing.T) {
	bin := t.TempDir()
	log := filepath.Join(bin, "log")
	script := `#!/bin/sh
echo "$*" >> ` + log + `
case "$*" in
  *"config --format json"*) echo '{"services":{"web":{"image":"x","ports":[{"target":80,"published":"8080","protocol":"tcp"}]}}}' ;;
esac
`
	os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	h := &helperServer{appsDir: t.TempDir(), webState: t.TempDir(), proofs: newProofKeeper()}
	meta := installedApp{ID: "wiki", Name: "Wiki", MainPort: 8080}
	dir := h.appDir("wiki")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  web:\n    image: x\n    ports: ['8080:80']\n"), 0o600)
	os.WriteFile(filepath.Join(dir, ".env"), nil, 0o600)
	h.writeMeta(meta)
	if err := h.applyBinding(meta, true, 8080, func(string) {}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, bindFile))
	if !strings.Contains(string(b), `"127.0.0.1:8080:80/tcp"`) {
		t.Fatalf("port not bound to loopback:\n%s", b)
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "-f "+filepath.Join(dir, bindFile)) || !strings.Contains(string(calls), "up -d") {
		t.Fatalf("not checked and recreated with the binding:\n%s", calls)
	}
	if err := h.applyBinding(meta, false, 8080, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dir, bindFile)) {
		t.Fatal("binding kept after the sign-in was turned off")
	}
	if err := h.applyBinding(meta, true, 9999, func(string) {}); err == nil || exists(filepath.Join(dir, bindFile)) {
		t.Fatalf("protecting a port the app does not publish: %v", err)
	}
}
