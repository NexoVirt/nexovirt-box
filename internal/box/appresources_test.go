package box

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDockerStatsParsing(t *testing.T) {
	for in, want := range map[string]int64{"0B": 0, "1.5kB": 1500, "12MiB": 12 << 20, "1.25GiB": 5 << 28, " 3MB ": 3e6, "x": 0} {
		if got := parseSize(in); got != want {
			t.Errorf("parseSize(%q) = %d, want %d", in, got, want)
		}
	}
	out := []byte(`{"ID":"aaaa111","CPUPerc":"1.50%","MemUsage":"100MiB / 512MiB","NetIO":"1kB / 2kB"}
{"ID":"bbbb222","CPUPerc":"0.50%","MemUsage":"50MiB / 7.6GiB","NetIO":"1kB / 1kB"}
{"ID":"cccc333","CPUPerc":"9%","MemUsage":"1GiB / 2GiB","NetIO":"0B / 0B"}`)
	got := parseStats(out, map[string]string{"aaaa111ffff": "web", "bbbb222ffff": "web"})
	u := got["web"]
	if len(got) != 1 || u.CPU != 2 || u.Mem != 150<<20 || u.MemLimit != 512<<20 || u.NetRx != 2000 || u.NetTx != 3000 {
		t.Fatalf("parsed %+v", got)
	}
}

// Limits are a second compose file with only cpus and mem_limit; anything else in that file
// (for example from a crafted backup) stops the app from starting.
func TestLimitsFile(t *testing.T) {
	if err := checkLimits(appLimits{CPUs: 0.05}, 4, 4096); err == nil {
		t.Fatal("tiny CPU limit accepted")
	}
	if err := checkLimits(appLimits{CPUs: 2, MemoryMB: 512}, 4, 4096); err != nil {
		t.Fatal(err)
	}
	if err := checkLimits(appLimits{MemoryMB: 8192}, 4, 4096); err == nil {
		t.Fatal("more memory than the server has accepted")
	}
	if _, err := limitsYAML([]string{"web", "bad name: x"}, appLimits{CPUs: 1}); err == nil {
		t.Fatal("odd service name accepted")
	}
	b, err := limitsYAML([]string{"web", "db"}, appLimits{CPUs: 1.5, MemoryMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, limitsFile), b, 0o600)
	if err := checkLimitsFile(dir); err != nil {
		t.Fatalf("own limits refused: %v\n%s", err, b)
	}
	for _, bad := range []string{
		"services:\n  web:\n    privileged: true\n",
		"services:\n  web:\n    mem_limit: 1g; x\n",
		"services:\n  web:\n    cpus: 1\nvolumes:\n  x: {}\n",
	} {
		os.WriteFile(filepath.Join(dir, limitsFile), []byte(bad), 0o600)
		if checkLimitsFile(dir) == nil {
			t.Errorf("accepted %q", bad)
		}
	}
	os.Remove(filepath.Join(dir, limitsFile))
	if checkLimitsFile(dir) != nil {
		t.Fatal("no file must be fine")
	}
}

// The limits path runs compose, so an own app whose compose file breaks the rules is refused
// before Docker reads it, like on every other path.
func TestLimitsRefuseUnsafeOwnApp(t *testing.T) {
	bin := t.TempDir()
	called := filepath.Join(bin, "called")
	os.WriteFile(filepath.Join(bin, "docker"), []byte("#!/bin/sh\ntouch "+called+"\nexit 1\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	h := &helperServer{appsDir: t.TempDir(), webState: t.TempDir(), proofs: newProofKeeper()}
	dir := h.appDir("evil")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  w:\n    build: /\n"), 0o600)
	os.WriteFile(filepath.Join(dir, ".env"), nil, 0o600)
	h.writeMeta(installedApp{ID: "evil", Name: "Evil", Custom: true, Reviewed: true})
	_, err := h.appLimitsSet(t.Context(), helperRequest{Action: "app_limits", ID: "evil", Limits: &appLimits{MemoryMB: 64}}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "builds an image") {
		t.Fatalf("unsafe own app got limits: %v", err)
	}
	if exists(called) {
		t.Fatal("Docker ran before the compose file was checked")
	}
	if exists(filepath.Join(dir, limitsFile)) {
		t.Fatal("limits.yaml left behind")
	}
}

// A compose file that passes the raw check but renders to something the policy refuses
// (privileged mode, not confirmed) is refused before `up`, and the old limits come back.
func TestLimitsRunInstallChecksBeforeUp(t *testing.T) {
	bin := t.TempDir()
	up := filepath.Join(bin, "up")
	script := `#!/bin/sh
case "$*" in
  *"config --services"*) echo w ;;
  *"config --format json"*) echo '{"services":{"w":{"image":"x","privileged":true}}}' ;;
  *" up "*) touch ` + up + ` ;;
esac
`
	os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	h := &helperServer{appsDir: t.TempDir(), webState: t.TempDir(), proofs: newProofKeeper()}
	dir := h.appDir("priv")
	os.MkdirAll(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte("services:\n  w:\n    image: x\n    privileged: true\n"), 0o600)
	os.WriteFile(filepath.Join(dir, ".env"), nil, 0o600)
	old := []byte("services:\n  w:\n    cpus: 1\n")
	os.WriteFile(filepath.Join(dir, limitsFile), old, 0o600)
	h.writeMeta(installedApp{ID: "priv", Name: "Priv", Custom: true, Reviewed: true})
	_, err := h.appLimitsSet(t.Context(), helperRequest{Action: "app_limits", ID: "priv", Limits: &appLimits{MemoryMB: 64}}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "privileged") {
		t.Fatalf("policy violation not refused: %v", err)
	}
	if exists(up) {
		t.Fatal("the app was recreated although the checks failed")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, limitsFile)); string(b) != string(old) {
		t.Fatalf("previous limits not restored: %q", b)
	}
}
