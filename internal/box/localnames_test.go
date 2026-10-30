package box

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalNamesScriptAndRoutes(t *testing.T) {
	s, err := publishScript([]string{"box.local", "wiki.box.local"}, "192.168.1.20")
	if err != nil || !strings.Contains(s, "avahi-publish -a -R wiki.box.local 192.168.1.20 &") || !strings.HasSuffix(s, "wait\n") {
		t.Fatalf("%v\n%s", err, s)
	}
	for _, bad := range [][]string{{"box.local", "a;rm -rf /.box.local"}, {"-x.box.local"}, {"box.local"}} {
		ip := "192.168.1.20"
		if len(bad) == 1 && bad[0] == "box.local" {
			ip = "fe80::1" // not IPv4
		}
		if _, err := publishScript(bad, ip); err == nil {
			t.Errorf("accepted %v %s", bad, ip)
		}
	}

	oldFw, oldProxy := firewallDir, proxyDir
	firewallDir, proxyDir = t.TempDir(), t.TempDir()
	t.Cleanup(func() { firewallDir, proxyDir = oldFw, oldProxy })
	writeJSONFile(fwPath("localnames.json"), localNamesConfig{Enabled: true})
	h := &helperServer{appsDir: t.TempDir()}
	for _, m := range []installedApp{
		{ID: "wiki", MainPort: 8080},
		{ID: "secret", MainPort: 9000, Domain: "s.example.com", Protect: true, DomainPort: 9000},
		{ID: "worker"},
	} {
		os.MkdirAll(h.appDir(m.ID), 0o700)
		h.writeMeta(m)
	}
	if got := strings.Join(h.localNames(), " "); got != "box.local wiki.box.local" {
		t.Fatalf("names %q (apps behind the sign-in or without a port get none)", got)
	}
	if err := h.writeCaddyfile(proxyConfig{HTTPPort: 80, HTTPSPort: 443, BoxPort: 8443}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(proxyDir, "Caddyfile"))
	if !strings.Contains(string(b), "http://wiki.box.local {\n\treverse_proxy 127.0.0.1:8080\n}") || strings.Contains(string(b), "secret.box.local") ||
		!strings.Contains(string(b), "redir https://box.local:8443{uri}") {
		t.Fatalf("Caddyfile:\n%s", b)
	}
}
