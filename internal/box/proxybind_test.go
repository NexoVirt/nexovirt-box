package box

import (
	"encoding/json"
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
	want := "services:\n  web:\n    ports: !override\n      - \"127.0.0.1:8080:80/tcp\"\n      - \"8443:443/tcp\"\n"
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

// fakeDockerPorts puts a docker on PATH that renders an app publishing port (and logs calls).
func fakeDockerPorts(t *testing.T, port string) string {
	bin := t.TempDir()
	log := filepath.Join(bin, "log")
	script := `#!/bin/sh
echo "$*" >> ` + log + `
case "$*" in
  *"config --format json"*) echo '{"services":{"web":{"image":"x","ports":[{"target":80,"published":"` + port + `","protocol":"tcp"}]}}}' ;;
  *"config --images"*) echo x ;;
  *"image inspect"*) echo sha256:abc ;;
esac
`
	os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return log
}

func protectedApp(t *testing.T, custom bool) (*helperServer, installedApp, string) {
	h := &helperServer{appsDir: t.TempDir(), webState: t.TempDir(), proofs: newProofKeeper()}
	meta := installedApp{ID: "wiki", Name: "Wiki", MainPort: 8080, Custom: custom, Reviewed: custom}
	dir := h.appDir("wiki")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  web:\n    image: x\n    ports: ['8080:80']\n"), 0o600)
	os.WriteFile(filepath.Join(dir, ".env"), nil, 0o600)
	h.writeMeta(meta)
	meta.Domain, meta.TLS, meta.Protect, meta.DomainPort = "wiki.example.com", "internal", true, 8080
	if err := h.applyBinding(meta, func(string) {}); err != nil {
		t.Fatal(err)
	}
	h.writeMeta(meta)
	return h, meta, dir
}

func loopbackBound(dir string) bool {
	b, _ := os.ReadFile(filepath.Join(dir, bindFile))
	return strings.Contains(string(b), `"127.0.0.1:8080:80/tcp"`)
}

// Turning the sign-in on binds the port to loopback (through the install checks), turning it off
// gives the normal binding back.
func TestApplyBindingProtectAndUnprotect(t *testing.T) {
	log := fakeDockerPorts(t, "8080")
	h, meta, dir := protectedApp(t, false)
	if !loopbackBound(dir) {
		t.Fatal("port not bound to loopback")
	}
	calls, _ := os.ReadFile(log)
	if !strings.Contains(string(calls), "-f "+filepath.Join(dir, bindFile)) || !strings.Contains(string(calls), "up -d") {
		t.Fatalf("not checked and recreated with the binding:\n%s", calls)
	}
	meta.Protect = false
	if err := h.applyBinding(meta, func(string) {}); err != nil {
		t.Fatal(err)
	}
	if exists(filepath.Join(dir, bindFile)) {
		t.Fatal("binding kept after the sign-in was turned off")
	}
	meta.Protect, meta.DomainPort = true, 9999
	if err := h.applyBinding(meta, func(string) {}); err == nil || exists(filepath.Join(dir, bindFile)) {
		t.Fatalf("protecting a port the app does not publish: %v", err)
	}
}

// Every later compose path keeps a protected port private: the binding is rewritten in
// checkConfig, which an edit, an update and a rollback all go through.
func TestProtectedPortStaysPrivate(t *testing.T) {
	fakeDockerPorts(t, "8080")
	t.Run("custom edit", func(t *testing.T) {
		h, meta, dir := protectedApp(t, true)
		os.Remove(filepath.Join(dir, bindFile)) // even a lost file comes back
		meta.Stopped = true
		h.writeMeta(meta)
		_, err := h.customEdit(t.Context(), helperRequest{ID: "wiki", Name: "Wiki", Compose: "services:\n  web:\n    image: y\n    ports: ['8080:80']\n"}, dir, func(string) {})
		if err != nil || !loopbackBound(dir) {
			t.Fatalf("edit: %v, loopback %v", err, loopbackBound(dir))
		}
	})
	t.Run("update", func(t *testing.T) {
		h, meta, dir := protectedApp(t, false)
		os.Remove(filepath.Join(dir, bindFile))
		meta.Stopped = true
		h.writeMeta(meta)
		if err := h.updateApp(t.Context(), meta, true, func() error {
			return checkConfig(t.Context(), dir, "wiki", h.policy(meta), false)
		}, func(string) {}); err != nil || !loopbackBound(dir) {
			t.Fatalf("update: %v, loopback %v", err, loopbackBound(dir))
		}
	})
	t.Run("rollback", func(t *testing.T) {
		h, meta, dir := protectedApp(t, false)
		// A copy from before the sign-in existed: no bind.yaml, no protect in its app.json.
		rb := h.rollbackDir("wiki")
		os.MkdirAll(rb, 0o700)
		copyTree(dir, filepath.Join(rb, "app"))
		os.Remove(filepath.Join(rb, "app", bindFile))
		old := meta
		old.Domain, old.Protect, old.DomainPort = "", false, 0
		b, _ := json.Marshal(old)
		os.WriteFile(filepath.Join(rb, "app", "app.json"), b, 0o600)
		os.WriteFile(filepath.Join(rb, "rollback.json"), []byte(`{"images":{}}`), 0o600)
		if err := h.rollbackApp(t.Context(), meta, func(string) {}); err != nil {
			t.Fatal(err)
		}
		if !loopbackBound(dir) {
			t.Fatal("rollback reopened the protected port")
		}
		if m, _ := h.readMeta("wiki"); !m.Protect || m.Domain != "wiki.example.com" {
			t.Fatalf("rollback dropped the domain settings: %+v", m)
		}
	})
	t.Run("port gone after edit", func(t *testing.T) {
		h, meta, dir := protectedApp(t, true)
		fakeDockerPorts(t, "9090")
		meta.Stopped = true
		h.writeMeta(meta)
		_, err := h.customEdit(t.Context(), helperRequest{ID: "wiki", Name: "Wiki", Compose: "services:\n  web:\n    image: y\n    ports: ['9090:80']\n"}, dir, func(string) {})
		if err == nil || !strings.Contains(err.Error(), "sign-in") {
			t.Fatalf("an edit that drops the protected port was accepted: %v", err)
		}
		if !loopbackBound(dir) {
			t.Fatal("the old binding was not kept")
		}
	})
}

// A protected app is never treated as "port 0": legacy app.json without the pinned port falls
// back to the main port, and without any port every compose change is refused while the old
// binding stays.
func TestProtectedPortNeverZero(t *testing.T) {
	fakeDockerPorts(t, "8080")
	h, meta, dir := protectedApp(t, false)
	// Legacy app.json: protected, no pinned port.
	legacy := meta
	legacy.DomainPort = 0
	b, _ := json.Marshal(legacy)
	os.WriteFile(filepath.Join(dir, "app.json"), b, 0o600)
	m, _ := h.readMeta("wiki")
	if m.DomainPort != 8080 || h.policy(m).protectPort != 8080 {
		t.Fatalf("legacy app not migrated: %+v / %d", m, h.policy(m).protectPort)
	}
	if err := checkConfig(t.Context(), dir, "wiki", h.policy(m), false); err != nil || !loopbackBound(dir) {
		t.Fatalf("legacy protected app lost its binding: %v", err)
	}
	// No port at all: fail closed.
	noPort := legacy
	noPort.MainPort = 0
	if p := h.policy(noPort).protectPort; p != -1 {
		t.Fatalf("protected app without a port got protectPort %d", p)
	}
	if err := checkConfig(t.Context(), dir, "wiki", h.policy(noPort), false); err == nil || !loopbackBound(dir) {
		t.Fatalf("binding removed for a protected app without a port: %v", err)
	}
	if err := syncBindFile(t.Context(), dir, "wiki", -1); err == nil || !loopbackBound(dir) {
		t.Fatal("syncBindFile did not fail closed")
	}
}

// Turning the sign-in on is refused when no port can be resolved.
func TestProtectRefusedWithoutPort(t *testing.T) {
	fakeDockerPorts(t, "8080")
	old := proxyDir
	proxyDir = t.TempDir()
	t.Cleanup(func() { proxyDir = old })
	os.WriteFile(filepath.Join(proxyDir, "proxy.json"), []byte(`{"http_port":80,"https_port":443}`), 0o600)
	h := &helperServer{appsDir: t.TempDir(), webState: t.TempDir(), proofs: newProofKeeper()}
	os.MkdirAll(h.appDir("noweb"), 0o700)
	h.writeMeta(installedApp{ID: "noweb", Name: "No web"})
	_, err := h.appDomain(helperRequest{ID: "noweb", Domain: "noweb.example.com", Confirm: "protect"}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "port") {
		t.Fatalf("protect without a port accepted: %v", err)
	}
	if m, _ := h.readMeta("noweb"); m.Protect || m.Domain != "" {
		t.Fatalf("settings written anyway: %+v", m)
	}
}
