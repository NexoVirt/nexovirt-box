package box

// Own apps: a compose file (and optional .env) the admin brings. It is stored verbatim like a
// catalog app and managed the same way, but because it is not signed by NexoVirt:
//
//   - adding or changing one needs a fresh password proof checked by the helper itself,
//   - the raw YAML is checked before Docker reads it (env_file, include, extends files, build,
//     secret and config files would make `docker compose config` read host files as root),
//   - the rendered config must not ask for root on the host (privileged, devices, host PID/IPC,
//     dangerous capabilities, unconfined security profiles), on top of the folder checks every
//     app gets (appsafety.go),
//   - .env may not steer Docker itself (DOCKER_*, COMPOSE_*).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	maxCompose = 256 << 10
	maxEnv     = 64 << 10
)

var errProofApp = userError{"Enter your password to add or change your own app."}

func slugID(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	return s
}

func checkOwnName(n string) error {
	if n = strings.TrimSpace(n); n == "" || utf8.RuneCountInString(n) > 60 || controlChar.MatchString(n) {
		return uerr("Give the app a name of up to 60 characters.")
	}
	return nil
}

func checkOwnEnv(env string) error {
	if len(env) > maxEnv {
		return uerr("The .env file can have at most 64 KB.")
	}
	if strings.ContainsRune(env, 0) || !utf8.ValidString(env) {
		return uerr("The .env file must be plain text.")
	}
	for _, l := range strings.Split(env, "\n") {
		k, _, ok := strings.Cut(strings.TrimSpace(l), "=")
		k = strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(k), "export ")))
		if ok && (strings.HasPrefix(k, "DOCKER_") || strings.HasPrefix(k, "COMPOSE_")) {
			return uerr("The .env file may not set %s: variables starting with DOCKER_ or COMPOSE_ change how Docker itself runs.", k)
		}
	}
	return nil
}

func isEnvDotFile(v any) bool {
	s, ok := v.(string)
	return ok && (s == ".env" || s == "./.env")
}

// checkOwnComposeRaw refuses everything that makes Docker read host files while it renders or
// starts the compose file.
func checkOwnComposeRaw(b []byte) error {
	if len(b) > maxCompose {
		return uerr("The compose file can have at most 256 KB.")
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return uerr("The compose file is not valid YAML: %s", strings.TrimPrefix(err.Error(), "yaml: "))
	}
	if _, ok := doc["include"]; ok {
		return uerr("include is not supported in own apps. Put everything into one compose file.")
	}
	for _, top := range []string{"secrets", "configs"} {
		m, _ := doc[top].(map[string]any)
		for name, v := range m {
			if e, _ := v.(map[string]any); e != nil && e["file"] != nil {
				return uerr("%s %s reads a file from the server. Use environment variables instead.", strings.TrimSuffix(top, "s"), name)
			}
		}
	}
	services, ok := doc["services"].(map[string]any)
	if !ok || len(services) == 0 {
		return uerr("The compose file has no services.")
	}
	for name, raw := range services {
		s, _ := raw.(map[string]any)
		if s == nil {
			continue
		}
		if _, ok := s["build"]; ok {
			return uerr("Service %s builds an image. Own apps must use ready images (image: ...).", name)
		}
		if _, ok := s["label_file"]; ok {
			return uerr("Service %s reads labels from a file. Write the labels into the compose file.", name)
		}
		if ext, _ := s["extends"].(map[string]any); ext != nil && ext["file"] != nil {
			return uerr("Service %s extends another file. Put everything into one compose file.", name)
		}
		switch ef := s["env_file"].(type) {
		case nil:
		case string:
			if !isEnvDotFile(ef) {
				return uerr("Service %s reads %s. Own apps can only use their own .env file.", name, ef)
			}
		case []any:
			for _, e := range ef {
				if m, ok := e.(map[string]any); ok {
					e = m["path"]
				}
				if !isEnvDotFile(e) {
					return uerr("Service %s reads another env file. Own apps can only use their own .env file.", name)
				}
			}
		default:
			return uerr("Service %s has an env_file Box cannot check.", name)
		}
	}
	return nil
}

// Capabilities that give a container control over the host.
var deniedCaps = map[string]bool{"ALL": true, "SYS_ADMIN": true, "SYS_MODULE": true, "SYS_RAWIO": true, "SYS_PTRACE": true,
	"SYS_BOOT": true, "DAC_READ_SEARCH": true, "MAC_ADMIN": true, "MAC_OVERRIDE": true, "BPF": true, "PERFMON": true,
	"SYSLOG": true, "SYS_TIME": true, "LINUX_IMMUTABLE": true}

var unconfinedRe = regexp.MustCompile(`(?i)unconfined|label[=:]disable|systempaths`)

// riskFinding is something in an own app's compose file that gives a container power over the
// server. Box refuses it unless the admin confirmed exactly this key (stored in app.json).
type riskFinding struct {
	Key  string `json:"key"`
	Text string `json:"text"`
}

// legacyOK are the findings Box allowed before the per-item review existed; own apps saved before
// it (app.json without "reviewed") keep running with them.
func legacyOK(key string) bool {
	return strings.HasSuffix(key, ":network_host") || strings.HasPrefix(key, "bind:")
}

// riskFindings reads `docker compose config --format json`. Binds outside the app's own folders,
// the shared folder and the file roots (p.roots) are findings too; binds of Box's state, system
// folders and secrets stay refused by checkMounts whatever the admin confirms.
func riskFindings(configJSON []byte, p mountPolicy) ([]riskFinding, error) {
	var cfg struct {
		Services map[string]struct {
			Privileged        bool     `json:"privileged"`
			CapAdd            []string `json:"cap_add"`
			Devices           []any    `json:"devices"`
			DeviceCgroupRules []string `json:"device_cgroup_rules"`
			NetworkMode       string   `json:"network_mode"`
			Pid               string   `json:"pid"`
			Ipc               string   `json:"ipc"`
			Uts               string   `json:"uts"`
			UsernsMode        string   `json:"userns_mode"`
			Cgroup            string   `json:"cgroup"`
			SecurityOpt       []string `json:"security_opt"`
			Gpus              any      `json:"gpus"`
			Volumes           []struct {
				Type   string `json:"type"`
				Source string `json:"source"`
			} `json:"volumes"`
		} `json:"services"`
	}
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return nil, uerr("The app definition could not be read.")
	}
	var out []riskFinding
	seen := map[string]bool{}
	add := func(key, text string) {
		if !seen[key] {
			seen[key] = true
			out = append(out, riskFinding{key, text})
		}
	}
	names := make([]string, 0, len(cfg.Services))
	for n := range cfg.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		s := cfg.Services[name]
		svc := func(what, text string) { add(name+":"+what, "Service "+name+" "+text) }
		if s.Privileged {
			svc("privileged", "runs in privileged mode (full control of the server).")
		}
		if len(s.Devices) > 0 || len(s.DeviceCgroupRules) > 0 {
			svc("devices", "uses host devices.")
		}
		if s.Gpus != nil {
			svc("gpus", "uses GPU devices.")
		}
		if s.NetworkMode == "host" {
			svc("network_host", "uses the server's network directly (every port it opens is open on the server).")
		}
		for k, v := range map[string]string{"pid": s.Pid, "ipc": s.Ipc, "uts": s.Uts, "userns": s.UsernsMode, "cgroup": s.Cgroup} {
			if v == "host" {
				svc(k+"_host", "shares the server's "+k+" namespace.")
			}
		}
		for _, c := range s.CapAdd {
			c = strings.TrimPrefix(strings.ToUpper(c), "CAP_")
			if deniedCaps[c] {
				svc("cap:"+c, "asks for the capability "+c+".")
			}
		}
		for _, o := range s.SecurityOpt {
			if unconfinedRe.MatchString(o) {
				svc("unconfined", "turns off a security profile ("+o+").")
			}
		}
		for _, v := range s.Volumes {
			if v.Type != "bind" || !filepath.IsAbs(v.Source) {
				continue
			}
			path := resolvePath(v.Source)
			if p.allowed(path) || p.inRoots(path) || filepath.Base(path) == "docker.sock" {
				continue
			}
			add("bind:"+filepath.Clean(v.Source), "A service mounts the server folder "+filepath.Clean(v.Source)+", which is outside Box's folders.")
		}
	}
	// Map order made the per-service namespace loop random; keep the list stable.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (p mountPolicy) inRoots(path string) bool {
	for _, r := range p.roots {
		r = resolvePath(r)
		if path == r || within(path, r) {
			return true
		}
	}
	return false
}

// checkPrivileges refuses every finding the admin did not confirm.
func checkPrivileges(configJSON []byte, p mountPolicy) error {
	fs, err := riskFindings(configJSON, p)
	if err != nil {
		return err
	}
	ok := map[string]bool{}
	for _, k := range p.confirmed {
		ok[k] = true
	}
	for _, f := range fs {
		if ok[f.Key] || (!p.reviewed && legacyOK(f.Key)) {
			continue
		}
		return uerr("%s Box refused it. Confirm it in the review step if you trust this app.", f.Text)
	}
	return nil
}

// customAppSave adds (op "add") or changes (op "edit") an own app and starts it.
func (h *helperServer) customAppSave(ctx context.Context, req helperRequest, logf func(string)) (any, error) {
	if !h.consoleAllowed(req) {
		return nil, errProofApp
	}
	if err := validID(req.ID); err != nil || req.ID == "box" {
		return nil, uerr("Use lowercase letters, digits and dashes for the app id.")
	}
	if err := checkOwnName(req.Name); err != nil {
		return nil, err
	}
	if err := checkOwnComposeRaw([]byte(req.Compose)); err != nil {
		return nil, err
	}
	if err := checkOwnEnv(req.Env); err != nil {
		return nil, err
	}
	if req.Icon != "" && !iconNameRe.MatchString(req.Icon) {
		return nil, uerr("Pick one of the offered icons.")
	}
	if err := needDocker(); err != nil {
		return nil, err
	}
	release, err := h.claim(req.ID, req.Op)
	if err != nil {
		return nil, err
	}
	defer release()
	ctx, cancel := context.WithTimeout(ctx, 40*time.Minute)
	defer cancel()
	dir := h.appDir(req.ID)
	switch req.Op {
	case "add":
		return h.customAdd(ctx, req, dir, logf)
	case "edit":
		return h.customEdit(ctx, req, dir, logf)
	}
	return nil, uerr("This action is not allowed.")
}

var iconNameRe = regexp.MustCompile(`^(lucide:[a-z0-9-]{1,40}|upload)$`)

func (h *helperServer) customAdd(ctx context.Context, req helperRequest, dir string, logf func(string)) (any, error) {
	if exists(dir) {
		return nil, uerr("An app with the id %s already exists. Pick another name.", req.ID)
	}
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		return nil, err
	}
	os.Chmod(filepath.Join(dir, "data"), 0o755)
	meta := installedApp{ID: req.ID, Name: strings.TrimSpace(req.Name), Version: "own", Scheme: "http", Store: "Own app",
		InstalledAt: time.Now().Unix(), DataDir: filepath.Join(dir, "data"), SocketOK: req.AllowDockerSocket, Custom: true, Icon: req.Icon,
		Allowed: req.Allow, Reviewed: true}
	if err := writeAppFiles(dir, req.Compose, req.Env, meta, true); err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	logf("Checking the compose file, ports and folders")
	cfg, err := checkConfigOut(ctx, dir, req.ID, h.policy(meta), nil)
	if err != nil {
		os.RemoveAll(dir) // nothing was started yet
		return nil, err
	}
	meta.MainPort = firstTCPPort(cfg)
	meta.Allowed = keepFound(cfg, h.policy(meta), req.Allow)
	h.writeMeta(meta)
	if err := h.up(ctx, meta.Name, dir, meta.ID, logf); err != nil {
		return nil, err // files stay: Retry install resumes, Uninstall removes them
	}
	return meta, nil
}

func (h *helperServer) customEdit(ctx context.Context, req helperRequest, dir string, logf func(string)) (any, error) {
	meta, err := h.readMeta(req.ID)
	if err != nil {
		return nil, err
	}
	if !meta.Custom {
		return nil, uerr("Only your own apps can be edited. Apps from the store keep their compose file.")
	}
	oldCfg, _ := renderConfig(ctx, dir, req.ID)
	prevCompose, _ := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	prevEnv, _ := os.ReadFile(filepath.Join(dir, ".env"))
	prevMeta := meta
	meta.Name, meta.SocketOK = strings.TrimSpace(req.Name), req.AllowDockerSocket
	meta.Allowed, meta.Reviewed = req.Allow, true
	if req.Icon != "" {
		meta.Icon = req.Icon
	}
	restore := func() {
		writeAppFiles(dir, string(prevCompose), string(prevEnv), prevMeta, false)
	}
	if err := writeAppFiles(dir, req.Compose, req.Env, meta, false); err != nil {
		restore()
		return nil, err
	}
	logf("Checking the new compose file, ports and folders")
	cfg, err := checkConfigOut(ctx, dir, req.ID, h.policy(meta), oldCfg)
	if err != nil {
		restore()
		return nil, err
	}
	meta.MainPort = firstTCPPort(cfg)
	meta.Allowed = keepFound(cfg, h.policy(meta), req.Allow)
	h.writeMeta(meta)
	if meta.Stopped {
		logf("Saved. The app stays stopped until you start it.")
		return meta, nil
	}
	logf("Starting " + meta.Name + " with the new definition")
	if err := runStream(ctx, compose(dir, req.ID, "up", "-d", "--remove-orphans"), logf); err != nil {
		logf("The new definition did not start. Going back to the previous one.")
		restore()
		runStream(context.Background(), compose(dir, req.ID, "up", "-d", "--remove-orphans"), logf)
		return nil, err
	}
	return meta, nil
}

// keepFound drops confirmations that match nothing in the config, so app.json lists only what the
// admin really allowed.
func keepFound(cfg []byte, p mountPolicy, allow []string) []string {
	fs, _ := riskFindings(cfg, p)
	want := map[string]bool{}
	for _, k := range allow {
		want[k] = true
	}
	var out []string
	for _, f := range fs {
		if want[f.Key] {
			out = append(out, f.Key)
		}
	}
	return out
}

// customAppCheck renders a compose file in a scratch folder and lists what needs a confirmation.
// Everything Box refuses outright comes back as the error.
func (h *helperServer) customAppCheck(req helperRequest, _ func(string)) (any, error) {
	if err := checkOwnComposeRaw([]byte(req.Compose)); err != nil {
		return nil, err
	}
	if err := checkOwnEnv(req.Env); err != nil {
		return nil, err
	}
	if err := needDocker(); err != nil {
		return nil, err
	}
	os.MkdirAll(h.appsDir, 0o700)
	dir, err := os.MkdirTemp(h.appsDir, ".check-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	meta := installedApp{ID: "check", Custom: true, Reviewed: true, SocketOK: true, DataDir: filepath.Join(dir, "data")}
	// No app.json here: nothing that lists apps may take the scratch folder for one.
	if err := os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(req.Compose), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(req.Env), 0o600); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cfg, err := renderConfig(ctx, dir, "check")
	if err != nil {
		return nil, err
	}
	p := h.policy(meta)
	p.allow = append(p.allow, dir)
	if err := checkMounts(cfg, p); err != nil {
		return nil, err
	}
	fs, err := riskFindings(cfg, p)
	if err != nil {
		return nil, err
	}
	return map[string]any{"findings": fs, "docker_socket": socketMounted(cfg)}, nil
}

func socketMounted(cfg []byte) bool { return strings.Contains(string(cfg), "docker.sock") }

// writeAppFiles stores the compose file and .env verbatim (0600) plus app.json.
func writeAppFiles(dir, composeText, env string, meta installedApp, marker bool) error {
	mb, _ := json.MarshalIndent(meta, "", "  ")
	files := map[string][]byte{"compose.yaml": []byte(composeText), ".env": []byte(env), "app.json": mb}
	if marker {
		files[installMarker] = nil
	}
	for name, body := range files {
		tmp := filepath.Join(dir, name+".tmp")
		if err := os.WriteFile(tmp, body, 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

// checkConfigOut runs the install checks and returns the rendered config. Only ports that the
// previous config (prev) did not publish yet must be free.
func checkConfigOut(ctx context.Context, dir, id string, p mountPolicy, prev []byte) ([]byte, error) {
	if err := checkConfig(ctx, dir, id, p, false); err != nil {
		return nil, err
	}
	cfg, err := renderConfig(ctx, dir, id)
	if err != nil {
		return nil, err
	}
	had := map[hostPort]bool{}
	if prev != nil {
		ps, _ := publishedPorts(prev)
		for _, hp := range ps {
			had[hp] = true
		}
	}
	ports, err := publishedPorts(cfg)
	if err != nil {
		return nil, err
	}
	for _, hp := range ports {
		if !had[hp] && !portFree(hp.proto, hp.port) {
			return nil, uerr("Port %d (%s) is already used on this server. Pick another port or stop what uses it.", hp.port, hp.proto)
		}
	}
	return cfg, nil
}

func firstTCPPort(cfg []byte) int {
	ps, _ := publishedPorts(cfg)
	best := 0
	for _, hp := range ps {
		if hp.proto == "tcp" && (best == 0 || hp.port < best) {
			best = hp.port
		}
	}
	return best
}

// customAppGet returns an own app's compose file and .env for editing.
func (h *helperServer) customAppGet(req helperRequest, _ func(string)) (any, error) {
	if err := validID(req.ID); err != nil {
		return nil, err
	}
	meta, err := h.readMeta(req.ID)
	if err != nil {
		return nil, err
	}
	if !meta.Custom {
		return nil, uerr("Only your own apps can be edited.")
	}
	c, _ := os.ReadFile(filepath.Join(h.appDir(req.ID), "compose.yaml"))
	e, _ := os.ReadFile(filepath.Join(h.appDir(req.ID), ".env"))
	return map[string]any{"id": meta.ID, "name": meta.Name, "icon": meta.Icon, "compose": string(c), "env": string(e),
		"docker_socket_ok": meta.SocketOK, "allowed": meta.Allowed}, nil
}
