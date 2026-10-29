package box

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestParseImage(t *testing.T) {
	for img, want := range map[string]imageRef{
		"nginx":                     {"registry-1.docker.io", "library/nginx", "latest"},
		"louislam/uptime-kuma:1":    {"registry-1.docker.io", "louislam/uptime-kuma", "1"},
		"ghcr.io/a/b:2.1":           {"ghcr.io", "a/b", "2.1"},
		"docker.io/library/redis:7": {"registry-1.docker.io", "library/redis", "7"},
		"localhost:5000/x":          {"localhost:5000", "x", "latest"},
	} {
		got, ok := parseImage(img)
		if !ok || got != want {
			t.Fatalf("%s: %+v", img, got)
		}
	}
	if _, ok := parseImage("nginx@sha256:abc"); ok {
		t.Fatal("pinned image should not be checked")
	}
}

func TestNewerVersion(t *testing.T) {
	for _, c := range [][3]any{{"0.2.0", "0.1.0-dev", true}, {"0.1.0", "0.1.0-dev", true}, {"0.1.0-dev", "0.1.0", false},
		{"0.1.10", "0.1.9", true}, {"0.1.0", "0.1.0", false}, {"1.0", "0.9.9", true}} {
		if newerVersion(c[0].(string), c[1].(string)) != c[2].(bool) {
			t.Fatalf("%v", c)
		}
	}
}

func TestCheckDataDir(t *testing.T) {
	for _, bad := range []string{"relative/x", "/", "/srv", "/etc/x", "/usr/local/x", "/root/data", "/var/lib/nexovirt-box/web/x", "/srv/../etc"} {
		if checkDataDir(bad) == nil {
			t.Fatalf("%s accepted", bad)
		}
	}
	dir := t.TempDir()
	if underAny(dir, systemDirs) {
		t.Skip("temp dir is under /tmp, which is a system folder for app data")
	}
	if err := checkDataDir(filepath.Join(dir, "new")); err != nil {
		t.Fatalf("new folder refused: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "f"), []byte("x"), 0o600)
	if checkDataDir(dir) == nil {
		t.Fatal("non-empty folder accepted")
	}
}

func TestWriteExecutableIgnoresUmask(t *testing.T) {
	old := syscall.Umask(0o077)
	defer syscall.Umask(old)
	p := filepath.Join(t.TempDir(), "bin")
	if err := writeExecutable(p, []byte("x")); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o755 {
		t.Fatalf("mode %v, the web user could not run it", st.Mode().Perm())
	}
}

func TestReleaseManifestSignature(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	old := releaseKey
	releaseKey = pub
	defer func() { releaseKey = old }()
	body := []byte(`{"version":"9.0.0","files":{}}`)
	sig := base64.StdEncoding.EncodeToString(ed25519.Sign(priv, body))
	bad := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/latest.json":
			if bad {
				w.Write([]byte(`{"version":"9.9.9","files":{}}`))
				return
			}
			w.Write(body)
		case "/latest.json.sig":
			w.Write([]byte(sig))
		}
	}))
	defer srv.Close()
	m, err := fetchManifest(t.Context(), srv.URL) // loopback http is allowed
	if err != nil || m.Version != "9.0.0" {
		t.Fatalf("signed manifest: %v %v", m, err)
	}
	bad = true
	if _, err := fetchManifest(t.Context(), srv.URL); err == nil {
		t.Fatal("changed manifest accepted")
	}
	if releaseURLOK("http://example.com/x") || !releaseURLOK("https://example.com/x") {
		t.Fatal("url rule wrong")
	}
}
