package box

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NexoVirt/nexovirt-box/internal/catalog"
)

// binds renders a minimal `docker compose config --format json` with bind mounts.
func binds(sources ...string) []byte {
	vols := []map[string]string{}
	for _, s := range sources {
		vols = append(vols, map[string]string{"type": "bind", "source": s, "target": "/x"})
	}
	b, _ := json.Marshal(map[string]any{"services": map[string]any{"app": map[string]any{"volumes": vols}}})
	return b
}

func testPolicy(t *testing.T) (mountPolicy, string) {
	t.Helper()
	base := t.TempDir()
	app := filepath.Join(base, "apps", "code-server")
	for _, d := range []string{filepath.Join(app, "data"), filepath.Join(base, "apps", "other"), filepath.Join(base, "web"), filepath.Join(base, "shared")} {
		os.MkdirAll(d, 0o700)
	}
	return mountPolicy{base: base, allow: []string{app, filepath.Join(base, "shared")}, appDir: app}, base
}

func TestMountsRefuseBoxState(t *testing.T) {
	p, base := testPolicy(t)
	for _, src := range []string{
		base,                                 // ROOT_FOLDER_HOST=/var/lib/nexovirt-box
		filepath.Dir(base),                   // a parent of the state dir
		filepath.Join(base, "web"),           // database and secrets
		filepath.Join(base, "web", "box.db"), // the database itself
		filepath.Join(base, "apps"),          // every app
		filepath.Join(base, "apps", "other"), // another app's folder
		filepath.Join(base, "apps", "other", ".env"),
		filepath.Join(base, "apps", "code-server"),             // its own folder: compose.yaml, .env, app.json
		filepath.Join(base, "apps", "code-server", "app.json"), // its own settings
		filepath.Join(base, "apps", "code-server", "compose.yaml"),
		"/etc/nexovirt-box", "/etc/nexovirt-box/caddy", "/run/nexovirt-box", "/run/nexovirt-box/helper.sock",
		"/", "/etc", "/root", "/root/.ssh", "/proc", "/sys", "/boot", "/run", "/etc/shadow", "/etc/sudoers.d",
	} {
		if err := checkMounts(binds(src), p); err == nil {
			t.Errorf("bind of %s was accepted", src)
		}
	}
}

func TestMountsAllowAppOwnAndShared(t *testing.T) {
	p, base := testPolicy(t)
	for _, src := range []string{
		filepath.Join(base, "apps", "code-server", "data", "config"),
		filepath.Join(base, "apps", "code-server", "data"),
		filepath.Join(base, "shared", "media"),
		"/etc/localtime", "/dev/dri", "/srv/media", "/var/log/auth.log",
	} {
		if err := checkMounts(binds(src), p); err != nil {
			t.Errorf("bind of %s refused: %v", src, err)
		}
	}
}

func TestMountsDockerSocketNeedsConsent(t *testing.T) {
	p, _ := testPolicy(t)
	for _, s := range []string{"/var/run/docker.sock", "/run/docker.sock"} {
		if err := checkMounts(binds(s), p); err == nil {
			t.Errorf("%s accepted without consent", s)
		}
	}
	p.allowSocket = true
	if err := checkMounts(binds("/var/run/docker.sock"), p); err != nil {
		t.Errorf("socket refused with consent: %v", err)
	}
}

func TestMountsFollowSymlinks(t *testing.T) {
	p, base := testPolicy(t)
	// A container wrote a symlink into its own data folder that points at Box's state.
	link := filepath.Join(base, "apps", "code-server", "data", "escape")
	if err := os.Symlink(filepath.Join(base, "web"), link); err != nil {
		t.Fatal(err)
	}
	if err := checkMounts(binds(link), p); err == nil {
		t.Fatal("a symlink to the state dir was accepted")
	}
	if err := checkMounts(binds(filepath.Join(link, "not-yet")), p); err == nil {
		t.Fatal("a missing path below a symlink to the state dir was accepted")
	}
	// A link from its data folder up to its own app folder (compose.yaml, app.json).
	up := filepath.Join(base, "apps", "code-server", "data", "db")
	os.Symlink("..", up)
	if err := checkMounts(binds(up), p); err == nil {
		t.Fatal("a symlink to the app's own folder was accepted")
	}
}

func TestMountsNamedVolumeBind(t *testing.T) {
	p, base := testPolicy(t)
	cfg, _ := json.Marshal(map[string]any{
		"services": map[string]any{"app": map[string]any{"volumes": []map[string]string{{"type": "volume", "source": "state", "target": "/x"}}}},
		"volumes":  map[string]any{"state": map[string]any{"driver": "local", "driver_opts": map[string]string{"type": "none", "o": "bind", "device": base}}},
	})
	if err := checkMounts(cfg, p); err == nil {
		t.Fatal("a local bind volume onto the state dir was accepted")
	}
}

func TestBuildEnvNeverPointsAtBoxState(t *testing.T) {
	base := "/var/lib/nexovirt-box"
	app := &catalog.App{Summary: catalog.Summary{ID: "code-server"}, MainPort: 8138}
	dir := filepath.Join(base, "apps", app.ID)
	env, _, err := buildEnv(app, nil, dir, filepath.Join(dir, "data"), base)
	if err != nil {
		t.Fatal(err)
	}
	vals := parseEnv(env)
	for _, k := range []string{"ROOT_FOLDER_HOST", "SHARED_DIR"} {
		v := vals[k]
		if v == "" || v == base || !strings.HasPrefix(v, filepath.Join(base, "shared")) {
			t.Errorf("%s = %q, want a folder below %s/shared", k, v, base)
		}
	}
}

func TestLegacyEnvIsRepointed(t *testing.T) {
	dir := t.TempDir()
	base := "/var/lib/nexovirt-box"
	os.WriteFile(filepath.Join(dir, ".env"), []byte("APP_ID='x'\nROOT_FOLDER_HOST='/var/lib/nexovirt-box'\nSHARED_DIR='/var/lib/nexovirt-box/shared'\n"), 0o600)
	if err := fixLegacyEnv(dir, base); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, ".env"))
	if v := parseEnv(b)["ROOT_FOLDER_HOST"]; v != sharedDir(base) {
		t.Fatalf("ROOT_FOLDER_HOST = %q", v)
	}
	if st, _ := os.Stat(filepath.Join(dir, ".env")); st.Mode().Perm() != 0o600 {
		t.Fatalf(".env mode %v", st.Mode().Perm())
	}
}

func TestPasswordsAreGenerated(t *testing.T) {
	app := &catalog.App{Summary: catalog.Summary{ID: "code-server"}, MainPort: 8138, Questions: []catalog.Question{
		{Key: "CODESERVER_PASSWORD", Label: "Password", Type: "password", Required: true, Min: 3, Max: 50},
		{Key: "CODESERVER_SUDO_PASSWORD", Label: "Sudo Password", Type: "password", Min: 3, Max: 50},
		{Key: "SHORT_PASSWORD", Label: "Short", Type: "password", Max: 12},
	}}
	// Nothing answered: every password gets a strong random value, nothing is "required".
	env, _, err := buildEnv(app, map[string]string{}, "/a", "/a/data", "/var/lib/nexovirt-box")
	if err != nil {
		t.Fatalf("passwords left empty were refused: %v", err)
	}
	v := parseEnv(env)
	if len(v["CODESERVER_PASSWORD"]) < 24 || len(v["CODESERVER_SUDO_PASSWORD"]) < 24 {
		t.Fatalf("weak generated passwords: %q %q", v["CODESERVER_PASSWORD"], v["CODESERVER_SUDO_PASSWORD"])
	}
	if n := len(v["SHORT_PASSWORD"]); n != 12 {
		t.Fatalf("generated password ignores max: %d", n)
	}
	// An own password is kept; the sudo password inside the app is always generated.
	env, _, _ = buildEnv(app, map[string]string{"CODESERVER_PASSWORD": "my-own-secret", "CODESERVER_SUDO_PASSWORD": "abc"}, "/a", "/a/data", "/var/lib/nexovirt-box")
	v = parseEnv(env)
	if v["CODESERVER_PASSWORD"] != "my-own-secret" || v["CODESERVER_SUDO_PASSWORD"] == "abc" {
		t.Fatalf("own/sudo password handling wrong: %v", v)
	}
	q := presentQuestions(app.Questions)
	if q[1].Label != "Password for sudo inside the app" || !q[1].Locked || q[1].Required || !q[0].Generate {
		t.Fatalf("questions not normalised: %+v", q)
	}
}
