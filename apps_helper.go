package main

// Root helper side of the app store: Docker only ever runs compose files from the signed
// catalog, written verbatim, with answers and secrets in a 0600 .env next to them.

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nexovirt/box/internal/catalog"
)

type installedApp struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Version     string `json:"version"`
	MainPort    int    `json:"main_port"`
	Scheme      string `json:"scheme"`
	Store       string `json:"store"`
	InstalledAt int64  `json:"installed_at"`
	Domain      string `json:"domain,omitempty"`
	TLS         string `json:"tls,omitempty"`
	DataDir     string `json:"data_dir,omitempty"`
	DataCreated bool   `json:"data_created,omitempty"` // Box created DataDir, so "delete data" may remove it
}

type appStatus struct {
	installedApp
	State      string            `json:"state"` // running, partial, stopped, restarting, unknown
	Containers []containerStatus `json:"containers"`
}

type containerStatus struct {
	Service string `json:"service"`
	State   string `json:"state"`
	Status  string `json:"status"`
}

func (h *helperServer) appLock(id string) func() {
	h.mu.Lock()
	if h.appLocks == nil {
		h.appLocks = map[string]*sync.Mutex{}
	}
	m := h.appLocks[id]
	if m == nil {
		m = &sync.Mutex{}
		h.appLocks[id] = m
	}
	h.mu.Unlock()
	m.Lock()
	return m.Unlock
}

func validID(id string) error {
	if !catalog.IDRe.MatchString(id) {
		return uerr("This app does not exist.")
	}
	return nil
}

func (h *helperServer) appDir(id string) string { return filepath.Join(h.appsDir, id) }

func compose(dir, id string, args ...string) *exec.Cmd {
	base := []string{"compose", "--project-name", "nxbox-" + id, "--project-directory", dir,
		"-f", filepath.Join(dir, "compose.yaml"), "--env-file", filepath.Join(dir, ".env")}
	cmd := exec.Command("docker", append(base, args...)...)
	cmd.Dir = dir
	return cmd
}

// runStream runs cmd, sends every output line to logf and returns a friendly error with the
// last line Docker printed when it fails.
func runStream(ctx context.Context, cmd *exec.Cmd, logf func(string)) error {
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return uerr("Docker could not be started: %v", err)
	}
	stop := context.AfterFunc(ctx, func() { cmd.Process.Kill() })
	defer stop()
	var last, hint string
	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			last = line
			if hint == "" {
				hint = dockerHint(line)
			}
			if logf != nil {
				logf(line)
			}
		}
		close(done)
	}()
	err := cmd.Wait()
	pw.Close()
	<-done
	if err != nil {
		if ctx.Err() != nil {
			return uerr("The action took too long and was stopped.")
		}
		if hint != "" {
			return userError{hint} // the raw Docker lines are in the job log
		}
		if last == "" {
			last = err.Error()
		}
		return uerr("Docker reported: %s", last)
	}
	return nil
}

// dockerHint turns a known Docker pull failure line into a friendly message ("" otherwise).
func dockerHint(line string) string {
	l := strings.ToLower(line)
	has := func(subs ...string) bool {
		for _, x := range subs {
			if strings.Contains(l, x) {
				return true
			}
		}
		return false
	}
	switch {
	case has("toomanyrequests", "pull rate limit", "rate limit exceeded"):
		return "Docker Hub is limiting downloads from this server right now. Try again in an hour or two."
	case has("no matching manifest for"):
		return "This app's image is not built for this server's processor (" + runtime.GOARCH + ")."
	case has("pull access denied", "repository does not exist", "manifest unknown", "requested access to the resource is denied") ||
		(has("manifest for") && has("not found")):
		return "This app's image is not available from its registry right now. It may have been removed or made private."
	case has("no space left on device"):
		return "The server ran out of disk space while downloading the app. Free some space and try again."
	case has("dial tcp", "i/o timeout", "tls handshake timeout", "no such host") && has("registry", "pull", "/v2/"):
		return "The image registry could not be reached. Check the server's internet connection and DNS."
	}
	return ""
}

func needDocker() error {
	if _, err := exec.LookPath("docker"); err != nil {
		return uerr("Docker is not installed on this server yet.")
	}
	return nil
}

// ---- docker ----

func (h *helperServer) dockerInfo(req helperRequest, logf func(string)) (any, error) {
	info := map[string]any{"installed": false}
	if _, err := exec.LookPath("docker"); err != nil {
		return info, nil
	}
	info["installed"] = true
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").Output(); err == nil {
		info["version"] = strings.TrimSpace(string(out))
		info["running"] = true
	} else {
		info["running"] = false
	}
	if out, err := exec.CommandContext(ctx, "docker", "compose", "version", "--short").Output(); err == nil {
		info["compose"] = strings.TrimSpace(string(out))
	}
	return info, nil
}

// dockerInstall uses Docker's official convenience script (get.docker.com). The web UI asks
// the admin to confirm first.
func (h *helperServer) dockerInstall(req helperRequest, logf func(string)) (any, error) {
	if _, err := exec.LookPath("docker"); err == nil {
		return nil, uerr("Docker is already installed.")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Minute)
	defer cancel()
	if _, err := exec.LookPath("curl"); err != nil {
		logf("Installing curl")
		if err := runStream(ctx, exec.Command("sh", "-c", "apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y curl ca-certificates"), logf); err != nil {
			return nil, err
		}
	}
	logf("Running the official Docker install script from get.docker.com")
	if err := runStream(ctx, exec.Command("sh", "-c", "curl -fsSL https://get.docker.com | sh"), logf); err != nil {
		return nil, err
	}
	runStream(ctx, exec.Command("systemctl", "enable", "--now", "docker"), logf)
	return h.dockerInfo(req, logf)
}

// ---- apps ----

func (h *helperServer) appInstall(req helperRequest, logf func(string)) (any, error) {
	if err := validID(req.ID); err != nil {
		return nil, err
	}
	defer h.appLock(req.ID)()
	if err := needDocker(); err != nil {
		return nil, err
	}
	cat, _, err := loadCatalog(h.webState)
	if err != nil {
		return nil, err
	}
	app, err := cat.App(req.ID)
	if err != nil {
		return nil, uerr("This app is not in the catalog.")
	}
	if !archSupported(app.Architectures) {
		return nil, uerr("%s is not available for %s servers.", app.Name, runtime.GOARCH)
	}
	dir := h.appDir(app.ID)
	if _, err := os.Stat(filepath.Join(dir, "compose.yaml")); err == nil {
		return nil, uerr("%s is already installed.", app.Name)
	}
	dataDir := filepath.Join(dir, "data")
	customData := req.DataDir != "" && req.DataDir != dataDir
	if customData {
		if err := checkDataDir(req.DataDir); err != nil {
			return nil, err
		}
		dataDir = req.DataDir
	}
	env, port, err := buildEnv(app, req.Answers, dir, dataDir, filepath.Dir(h.appsDir))
	if err != nil {
		return nil, err
	}
	_, statErr := os.Stat(dir)
	fresh := os.IsNotExist(statErr) // a kept data folder from an earlier install is reused
	_, dataErr := os.Stat(dataDir)
	dataCreated := customData && os.IsNotExist(dataErr)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	os.Chmod(dataDir, 0o755)
	cleanup := func() {
		if dataCreated {
			os.RemoveAll(dataDir)
		}
		if fresh {
			os.RemoveAll(dir)
			return
		}
		for _, f := range []string{"compose.yaml", ".env", "app.json"} {
			os.Remove(filepath.Join(dir, f))
		}
	}
	meta := installedApp{ID: app.ID, Name: app.Name, Version: app.Version, MainPort: port, Scheme: app.Scheme,
		Store: app.Attribution.Store, InstalledAt: time.Now().Unix(), DataDir: dataDir, DataCreated: dataCreated}
	metaJSON, _ := json.MarshalIndent(meta, "", "  ")
	for name, body := range map[string][]byte{"compose.yaml": []byte(app.Compose), ".env": env, "app.json": metaJSON} {
		if err := os.WriteFile(filepath.Join(dir, name), body, 0o600); err != nil {
			cleanup()
			return nil, err
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	logf("Checking ports")
	if err := checkPorts(ctx, dir, app.ID); err != nil {
		cleanup()
		return nil, err
	}
	logf("Downloading and starting " + app.Name + ". The first download can take a few minutes.")
	if err := runStream(ctx, compose(dir, app.ID, "up", "-d", "--remove-orphans"), logf); err != nil {
		down, dcancel := context.WithTimeout(context.Background(), 2*time.Minute)
		runStream(down, compose(dir, app.ID, "down", "--remove-orphans"), nil)
		dcancel()
		cleanup()
		return nil, err
	}
	logf(app.Name + " is running.")
	return meta, nil
}

var (
	envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	emailRe  = regexp.MustCompile(`^[^\s@]+@[^\s@]+\.[^\s@]+$`)
	domainRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$`)
)

// buildEnv validates the answers and renders the .env file. Single quotes are not allowed in
// answers so every value can be written as KEY='value' (no interpolation, no escaping).
func buildEnv(app *catalog.App, answers map[string]string, dir, dataDir, base string) ([]byte, int, error) {
	ip := serverIP()
	vals := map[string]string{
		"APP_ID": app.ID, "APP_DATA_DIR": dataDir, "APP_PORT": strconv.Itoa(app.MainPort),
		"APP_DOMAIN": ip, "APP_HOST": ip, "INTERNAL_IP": ip, "NETWORK_INTERFACE": ip, "APP_PROTOCOL": "http",
		"LOCAL_DOMAIN": "local", "DNS_IP": "1.1.1.1", "APP_EXPOSED": "false",
		"TZ": timezone(), "PUID": "1000", "PGID": "1000",
		"SHARED_DIR": filepath.Join(base, "shared"), "ROOT_FOLDER_HOST": base,
	}
	for _, q := range app.Questions {
		if !envKeyRe.MatchString(q.Key) {
			return nil, 0, uerr("The app definition is not valid.")
		}
		v := strings.TrimSpace(answers[q.Key])
		if strings.ContainsAny(v, "'\r\n\x00") || strings.IndexFunc(v, func(r rune) bool { return r < 0x20 }) >= 0 {
			return nil, 0, uerr("%s cannot contain single quotes or line breaks.", q.Label)
		}
		if v == "" && q.Generate {
			v = randomSecret(max(q.Min, 32))
		}
		if v == "" {
			v = q.Default
		}
		if v == "" {
			if q.Required {
				return nil, 0, uerr("%s is required.", q.Label)
			}
			vals[q.Key] = ""
			continue
		}
		switch q.Type {
		case "port":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 65535 {
				return nil, 0, uerr("%s must be a port between 1 and 65535.", q.Label)
			}
		case "number":
			n, err := strconv.Atoi(v)
			if err != nil {
				return nil, 0, uerr("%s must be a number.", q.Label)
			}
			if (q.Min != 0 && n < q.Min) || (q.Max != 0 && n > q.Max) {
				return nil, 0, uerr("%s must be between %d and %d.", q.Label, q.Min, q.Max)
			}
		case "email":
			if !emailRe.MatchString(v) {
				return nil, 0, uerr("%s must be an email address.", q.Label)
			}
		case "domain":
			if !domainRe.MatchString(v) {
				return nil, 0, uerr("%s must be a domain name or IP address.", q.Label)
			}
		case "boolean":
			if v != "true" && v != "false" {
				return nil, 0, uerr("%s must be on or off.", q.Label)
			}
		case "select":
			ok := false
			for _, o := range q.Options {
				ok = ok || o == v
			}
			if !ok {
				return nil, 0, uerr("Pick one of the offered values for %s.", q.Label)
			}
		default:
			if q.Min > 0 && len(v) < q.Min {
				return nil, 0, uerr("%s needs at least %d characters.", q.Label, q.Min)
			}
			if q.Max > 0 && len(v) > q.Max {
				return nil, 0, uerr("%s can have at most %d characters.", q.Label, q.Max)
			}
		}
		vals[q.Key] = v
	}
	port, _ := strconv.Atoi(vals["APP_PORT"])
	keys := make([]string, 0, len(vals))
	for k := range vals {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString("# Written by NexoVirt Box. Holds secrets: keep it private.\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "%s='%s'\n", k, vals[k])
	}
	return []byte(b.String()), port, nil
}

func randomSecret(n int) string {
	const chars = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	out := make([]byte, n)
	for i := range out {
		k, _ := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		out[i] = chars[k.Int64()]
	}
	return string(out)
}

func archSupported(list []string) bool {
	if len(list) == 0 {
		return true
	}
	for _, a := range list {
		if a == runtime.GOARCH {
			return true
		}
	}
	return false
}

func serverIP() string {
	c, err := net.Dial("udp", "1.1.1.1:80") // no packet is sent; picks the outgoing interface
	if err != nil {
		return "127.0.0.1"
	}
	defer c.Close()
	return c.LocalAddr().(*net.UDPAddr).IP.String()
}

func timezone() string {
	if b, err := os.ReadFile("/etc/timezone"); err == nil && strings.TrimSpace(string(b)) != "" {
		return strings.TrimSpace(string(b))
	}
	if t, err := os.Readlink("/etc/localtime"); err == nil {
		if i := strings.Index(t, "zoneinfo/"); i >= 0 {
			return t[i+9:]
		}
	}
	return "UTC"
}

// checkPorts renders the compose config and refuses host ports that are already in use.
func checkPorts(ctx context.Context, dir, id string) error {
	cmd := compose(dir, id, "config", "--format", "json")
	out, err := cmd.Output()
	if err != nil {
		msg := ""
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			msg = strings.TrimSpace(lastLine(string(ee.Stderr)))
		}
		return uerr("The app definition could not be read by Docker: %s", msg)
	}
	ports, err := publishedPorts(out)
	if err != nil {
		return err
	}
	for _, p := range ports {
		if !portFree(p.proto, p.port) {
			return uerr("Port %d (%s) is already used on this server. Pick another port or stop what uses it.", p.port, p.proto)
		}
	}
	return nil
}

type hostPort struct {
	proto string
	port  int
}

func publishedPorts(configJSON []byte) ([]hostPort, error) {
	var cfg struct {
		Services map[string]struct {
			Ports []struct {
				Published any    `json:"published"`
				Protocol  string `json:"protocol"`
			} `json:"ports"`
		} `json:"services"`
	}
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return nil, uerr("The app definition could not be read.")
	}
	var out []hostPort
	for _, s := range cfg.Services {
		for _, p := range s.Ports {
			pub := strings.TrimSpace(fmt.Sprint(p.Published))
			if pub == "" || pub == "<nil>" || pub == "0" {
				continue
			}
			proto := p.Protocol
			if proto == "" {
				proto = "tcp"
			}
			lo, hi := pub, pub
			if a, b, ok := strings.Cut(pub, "-"); ok {
				lo, hi = a, b
			}
			l, err1 := strconv.Atoi(lo)
			u, err2 := strconv.Atoi(hi)
			if err1 != nil || err2 != nil || l < 1 || u > 65535 || u < l {
				continue
			}
			for n := l; n <= u && n-l < 1000; n++ {
				out = append(out, hostPort{proto, n})
			}
		}
	}
	return out, nil
}

func portFree(proto string, port int) bool {
	addr := ":" + strconv.Itoa(port)
	if proto == "udp" {
		c, err := net.ListenPacket("udp", addr)
		if err != nil {
			return false
		}
		c.Close()
		return true
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return false
	}
	l.Close()
	return true
}

func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1]
}

func (h *helperServer) readMeta(id string) (installedApp, error) {
	var m installedApp
	b, err := os.ReadFile(filepath.Join(h.appDir(id), "app.json"))
	if err != nil {
		return m, uerr("This app is not installed.")
	}
	err = json.Unmarshal(b, &m)
	return m, err
}

func (h *helperServer) appsStatus(req helperRequest, logf func(string)) (any, error) {
	entries, _ := os.ReadDir(h.appsDir)
	out := []appStatus{}
	haveDocker := needDocker() == nil
	for _, e := range entries {
		if !e.IsDir() || validID(e.Name()) != nil {
			continue
		}
		meta, err := h.readMeta(e.Name())
		if err != nil {
			continue
		}
		st := appStatus{installedApp: meta, State: "unknown", Containers: []containerStatus{}}
		if haveDocker {
			st.Containers, st.State = composeStatus(h.appDir(meta.ID), meta.ID)
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func composeStatus(dir, id string) ([]containerStatus, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := compose(dir, id, "ps", "--all", "--format", "json")
	out, err := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...).Output()
	if err != nil {
		return []containerStatus{}, "unknown"
	}
	type psRow struct{ Service, State, Status string }
	var rows []psRow
	trimmed := strings.TrimSpace(string(out))
	if strings.HasPrefix(trimmed, "[") {
		json.Unmarshal([]byte(trimmed), &rows)
	} else {
		for _, l := range strings.Split(trimmed, "\n") {
			var r psRow
			if json.Unmarshal([]byte(l), &r) == nil && r.Service != "" {
				rows = append(rows, r)
			}
		}
	}
	cs := []containerStatus{}
	running, restarting := 0, 0
	for _, r := range rows {
		cs = append(cs, containerStatus{Service: r.Service, State: r.State, Status: r.Status})
		switch r.State {
		case "running":
			running++
		case "restarting":
			restarting++
		}
	}
	state := "stopped"
	switch {
	case len(rows) == 0:
		state = "stopped"
	case restarting > 0:
		state = "restarting"
	case running == len(rows):
		state = "running"
	case running > 0:
		state = "partial"
	}
	return cs, state
}

func (h *helperServer) appAction(req helperRequest, logf func(string)) (any, error) {
	if err := validID(req.ID); err != nil {
		return nil, err
	}
	defer h.appLock(req.ID)()
	if err := needDocker(); err != nil {
		return nil, err
	}
	meta, err := h.readMeta(req.ID)
	if err != nil {
		return nil, err
	}
	dir := h.appDir(req.ID)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Minute)
	defer cancel()
	switch req.Op {
	case "start":
		err = runStream(ctx, compose(dir, req.ID, "up", "-d", "--remove-orphans"), logf)
	case "stop":
		err = runStream(ctx, compose(dir, req.ID, "stop"), logf)
	case "restart":
		err = runStream(ctx, compose(dir, req.ID, "restart"), logf)
	case "update":
		logf("Downloading the newest images of " + meta.Name)
		if err = runStream(ctx, compose(dir, req.ID, "pull"), logf); err == nil {
			err = runStream(ctx, compose(dir, req.ID, "up", "-d", "--remove-orphans"), logf)
		}
	default:
		return nil, uerr("This action is not allowed.")
	}
	if err != nil {
		return nil, err
	}
	_, state := composeStatus(dir, req.ID)
	return map[string]string{"state": state}, nil
}

func (h *helperServer) appUninstall(req helperRequest, logf func(string)) (any, error) {
	if err := validID(req.ID); err != nil {
		return nil, err
	}
	defer h.appLock(req.ID)()
	meta, err := h.readMeta(req.ID)
	if err != nil {
		return nil, err
	}
	dir := h.appDir(req.ID)
	if needDocker() == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		args := []string{"down", "--remove-orphans"}
		if req.DeleteData {
			args = append(args, "--volumes")
		}
		err := runStream(ctx, compose(dir, req.ID, args...), logf)
		cancel()
		if err != nil {
			return nil, err
		}
	}
	dataDir := meta.DataDir
	if dataDir == "" {
		dataDir = filepath.Join(dir, "data")
	}
	defer func() {
		if meta.Domain != "" {
			h.reloadProxy(logf) // its route is gone with app.json
		}
	}()
	if req.DeleteData {
		if meta.DataCreated { // only a folder Box created itself is deleted
			if err := os.RemoveAll(dataDir); err != nil {
				return nil, err
			}
		}
		if err := os.RemoveAll(dir); err != nil {
			return nil, err
		}
		logf(meta.Name + " and its data were removed.")
		return map[string]any{"data_kept": false}, nil
	}
	for _, f := range []string{"compose.yaml", ".env", "app.json"} {
		os.Remove(filepath.Join(dir, f))
	}
	logf(meta.Name + " was removed. Its data stays in " + dataDir + ".")
	return map[string]any{"data_kept": true, "data_dir": dataDir}, nil
}

func (h *helperServer) appLogs(req helperRequest, logf func(string)) (any, error) {
	if err := validID(req.ID); err != nil {
		return nil, err
	}
	if err := needDocker(); err != nil {
		return nil, err
	}
	if _, err := h.readMeta(req.ID); err != nil {
		return nil, err
	}
	tail := req.Tail
	if tail <= 0 || tail > 2000 {
		tail = 300
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cmd := compose(h.appDir(req.ID), req.ID, "logs", "--no-color", "--timestamps", "--tail", strconv.Itoa(tail))
	out, err := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...).CombinedOutput()
	if err != nil && len(out) == 0 {
		return nil, uerr("The logs could not be read.")
	}
	if len(out) > 512*1024 {
		out = out[len(out)-512*1024:]
	}
	return map[string]string{"logs": string(out)}, nil
}

// systemDirs never hold app data (and nothing below them).
var systemDirs = []string{"/etc", "/usr", "/bin", "/sbin", "/lib", "/lib32", "/lib64", "/boot", "/proc", "/sys",
	"/dev", "/run", "/root", "/var/lib/docker", "/var/lib/nexovirt-box/web", "/var/log", "/tmp"}

func underAny(p string, dirs []string) bool {
	for _, d := range dirs {
		if p == d || strings.HasPrefix(p, d+"/") {
			return true
		}
	}
	return false
}

// checkDataDir accepts an absolute folder outside system paths that is new or empty.
func checkDataDir(p string) error {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p || strings.Count(p, "/") < 2 {
		return uerr("Use a full folder path like /srv/apps/name.")
	}
	if underAny(p, systemDirs) {
		return uerr("%s is a system folder. Pick a folder under /srv, /home, /opt or /mnt.", p)
	}
	st, err := os.Lstat(p)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !st.IsDir() {
		return uerr("%s exists and is not a folder.", p)
	}
	entries, _ := os.ReadDir(p)
	if len(entries) > 0 {
		return uerr("%s is not empty. Pick an empty or new folder.", p)
	}
	return nil
}

// ---- unused images ----

func danglingImages() ([]string, int64) {
	out, err := exec.Command("docker", "images", "--filter", "dangling=true", "--quiet", "--no-trunc").Output()
	if err != nil {
		return nil, 0
	}
	ids := strings.Fields(string(out))
	if len(ids) == 0 {
		return nil, 0
	}
	sizes, _ := exec.Command("docker", append([]string{"image", "inspect", "--format", "{{.Size}}"}, ids...)...).Output()
	var total int64
	for _, s := range strings.Fields(string(sizes)) {
		n, _ := strconv.ParseInt(s, 10, 64)
		total += n
	}
	return ids, total
}

func (h *helperServer) imagesUnused(req helperRequest, logf func(string)) (any, error) {
	if needDocker() != nil {
		return map[string]any{"count": 0, "bytes": 0}, nil
	}
	ids, total := danglingImages()
	return map[string]any{"count": len(ids), "bytes": total}, nil
}

func (h *helperServer) imagesPrune(req helperRequest, logf func(string)) (any, error) {
	if err := needDocker(); err != nil {
		return nil, err
	}
	ids, total := danglingImages()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := runStream(ctx, exec.Command("docker", "image", "prune", "--force"), logf); err != nil {
		return nil, err
	}
	return map[string]any{"count": len(ids), "bytes": total}, nil
}
