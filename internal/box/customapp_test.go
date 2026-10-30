package box

import (
	"strings"
	"testing"
)

// The raw compose file is checked before Docker reads it: `docker compose config` itself would
// read env_file, include, extends and build contexts as root.
func TestOwnComposeRawRefusals(t *testing.T) {
	ok := []string{
		"services:\n  web:\n    image: nginx\n    ports: ['8080:80']\n    volumes: ['./html:/usr/share/nginx/html']\n",
		"services:\n  web:\n    image: traefik/whoami\n    env_file: .env\n",
		"services:\n  web:\n    image: traefik/whoami\n    env_file: [./.env]\nx-note: hi\nvolumes:\n  db: {}\n",
		"name: mine\nservices:\n  a:\n    image: busybox\n  b:\n    image: busybox\n    extends:\n      service: a\n",
	}
	for _, y := range ok {
		if err := checkOwnComposeRaw([]byte(y)); err != nil {
			t.Errorf("refused a normal compose file: %v\n%s", err, y)
		}
	}
	bad := map[string]string{
		"env_file":      "services:\n  w:\n    image: x\n    env_file: /etc/shadow\n",
		"env_file_long": "services:\n  w:\n    image: x\n    env_file:\n      - path: /root/.ssh/id_rsa\n",
		"include":       "include:\n  - /etc/nexovirt-box/x.yaml\nservices:\n  w:\n    image: x\n",
		"extends_file":  "services:\n  w:\n    extends:\n      file: /etc/x.yml\n      service: y\n",
		"build":         "services:\n  w:\n    build: /\n",
		"secret_file":   "services:\n  w:\n    image: x\nsecrets:\n  s:\n    file: /etc/shadow\n",
		"config_file":   "services:\n  w:\n    image: x\nconfigs:\n  c:\n    file: /etc/ssh/ssh_host_ed25519_key\n",
		"label_file":    "services:\n  w:\n    image: x\n    label_file: /etc/shadow\n",
		"not_yaml":      "services: [\n",
		"no_services":   "volumes: {}\n",
		"anchor_bomb":   "a: &a [x,x,x,x,x,x,x,x,x]\nb: &b [*a,*a,*a,*a,*a,*a,*a,*a,*a]\nc: &c [*b,*b,*b,*b,*b,*b,*b,*b,*b]\nd: &d [*c,*c,*c,*c,*c,*c,*c,*c,*c]\ne: &e [*d,*d,*d,*d,*d,*d,*d,*d,*d]\nf: &f [*e,*e,*e,*e,*e,*e,*e,*e,*e]\ng: [*f,*f,*f,*f,*f,*f,*f,*f,*f]\nservices:\n  w:\n    image: x\n",
	}
	for name, y := range bad {
		if err := checkOwnComposeRaw([]byte(y)); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// After `docker compose config`, everything that would give the container root on the host
// is refused (mounts are checked by checkMounts as for catalog apps).
func TestOwnComposePrivileges(t *testing.T) {
	ok := `{"services":{"w":{"image":"nginx","cap_add":["NET_ADMIN"],"volumes":[{"type":"bind","source":"/srv/media"}]}}}`
	p := mountPolicy{custom: true, reviewed: true, roots: []string{"/srv"}}
	if err := checkPrivileges([]byte(ok), p); err != nil {
		t.Fatalf("normal app refused: %v", err)
	}
	bad := map[string]string{
		"privileged":    `{"services":{"w":{"privileged":true}}}`,
		"cap_all":       `{"services":{"w":{"cap_add":["ALL"]}}}`,
		"cap_sysadm":    `{"services":{"w":{"cap_add":["CAP_SYS_ADMIN"]}}}`,
		"cap_lower":     `{"services":{"w":{"cap_add":["sys_module"]}}}`,
		"devices":       `{"services":{"w":{"devices":[{"source":"/dev/sda","target":"/dev/sda"}]}}}`,
		"pid":           `{"services":{"w":{"pid":"host"}}}`,
		"ipc":           `{"services":{"w":{"ipc":"host"}}}`,
		"userns":        `{"services":{"w":{"userns_mode":"host"}}}`,
		"cgroup":        `{"services":{"w":{"cgroup":"host"}}}`,
		"apparmor":      `{"services":{"w":{"security_opt":["apparmor=unconfined"]}}}`,
		"seccomp":       `{"services":{"w":{"security_opt":["seccomp:unconfined"]}}}`,
		"label":         `{"services":{"w":{"security_opt":["label=disable"]}}}`,
		"cgroup_rule":   `{"services":{"w":{"device_cgroup_rules":["a *:* rwm"]}}}`,
		"gpus":          `{"services":{"w":{"gpus":"all"}}}`,
		"volumes_from":  `{"services":{"w":{"volumes_from":["container:nxbox-portainer-portainer-1"]}}}`,
		"net_container": `{"services":{"w":{"network_mode":"container:nxbox-other-1"}}}`,
		"pid_container": `{"services":{"w":{"pid":"container:nxbox-other-1"}}}`,
	}
	for name, j := range bad {
		if err := checkPrivileges([]byte(j), p); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

// Every risky item can be confirmed one by one, and only the confirmed key passes.
func TestOwnComposeConfirmPerItem(t *testing.T) {
	cfg := []byte(`{"services":{"w":{"privileged":true,"network_mode":"host","volumes":[{"type":"bind","source":"/opt/stuff"}]},"x":{"cap_add":["CAP_SYS_ADMIN"]}}}`)
	p := mountPolicy{custom: true, reviewed: true, roots: []string{"/srv"}}
	fs, err := riskFindings(cfg, p)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, f := range fs {
		keys = append(keys, f.Key)
	}
	want := "bind:/opt/stuff w:network_host w:privileged x:cap:SYS_ADMIN"
	if strings.Join(keys, " ") != want {
		t.Fatalf("findings %v, want %s", keys, want)
	}
	p.confirmed = keys[:3]
	if err := checkPrivileges(cfg, p); err == nil || !strings.Contains(err.Error(), "SYS_ADMIN") {
		t.Fatalf("unconfirmed capability passed: %v", err)
	}
	p.confirmed = keys
	if err := checkPrivileges(cfg, p); err != nil {
		t.Fatalf("confirmed items refused: %v", err)
	}
	// Own apps saved before the review keep host network and outside binds, never privileges.
	legacy := mountPolicy{custom: true}
	if err := checkPrivileges([]byte(`{"services":{"w":{"network_mode":"host","volumes":[{"type":"bind","source":"/opt/stuff"}]}}}`), legacy); err != nil {
		t.Fatalf("legacy app refused: %v", err)
	}
	if err := checkPrivileges([]byte(`{"services":{"w":{"privileged":true}}}`), legacy); err == nil {
		t.Fatal("legacy app got privileged mode")
	}
	// Box's own files and system folders stay refused whatever was confirmed.
	if err := checkMounts([]byte(`{"services":{"w":{"volumes":[{"type":"bind","source":"/etc"}]}}}`), p); err == nil {
		t.Fatal("/etc mounted")
	}
}

func TestOwnEnvRefusals(t *testing.T) {
	if err := checkOwnEnv("TZ=Europe/Berlin\n# note\nPASSWORD='x y'\n"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"DOCKER_HOST=tcp://evil:2375\n", "COMPOSE_FILE=/etc/x.yml\n", " docker_config=/x\n", "A=b\x00\n", strings.Repeat("A", 70<<10)} {
		if err := checkOwnEnv(bad); err == nil {
			t.Errorf("accepted %.40q", bad)
		}
	}
}

func TestOwnAppNames(t *testing.T) {
	if id := slugID("My Whoami!"); id != "my-whoami" {
		t.Fatalf("slug: %q", id)
	}
	if id := slugID("..."); id != "" {
		t.Fatalf("slug of nothing: %q", id)
	}
	if err := checkOwnName("Nginx test"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "a\nb", strings.Repeat("x", 61)} {
		if checkOwnName(bad) == nil {
			t.Errorf("name %q accepted", bad)
		}
	}
}

// Adding, editing and restoring an app from an archive need a password proof checked by the
// helper itself (a compromised web process cannot do it alone).
func TestOwnAppNeedsProof(t *testing.T) {
	h := &helperServer{appsDir: t.TempDir(), webState: t.TempDir(), proofs: newProofKeeper()}
	_, err := h.customAppSave(t.Context(), helperRequest{Action: "custom_app_save", Op: "add", ID: "web", Name: "Web",
		Compose: "services:\n  w:\n    image: nginx\n"}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("saved without a proof: %v", err)
	}
}
