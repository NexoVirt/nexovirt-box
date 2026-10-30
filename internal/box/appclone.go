package box

// Clone an installed app: a second instance with its own id and name, its own ports, its own
// data folder and (through the new compose project name) its own named volumes. The compose
// file is copied verbatim; Box's variables in .env are repointed at the clone, and a Box-written
// clone.yaml (an extra compose file like limits.yaml) gives fixed container names a clone prefix
// and moves published ports that are taken to free ones. A clone that would share a host folder
// with the original (an absolute bind outside its own folder and the shared folder) is refused.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const cloneFile = "clone.yaml"

type cloneService struct {
	ContainerName string      `json:"container_name"`
	Ports         []portEntry `json:"ports"`
	Volumes       []struct {
		Type   string `json:"type"`
		Source string `json:"source"`
	} `json:"volumes"`
}

// cloneYAML builds the override: container names prefixed with the clone's project and every
// published port mapped through ports (old host port -> new). It reports an error for port
// ranges, which cannot be moved one by one.
func cloneYAML(configJSON []byte, id string, ports map[int]int) ([]byte, error) {
	var cfg struct {
		Services map[string]cloneService `json:"services"`
	}
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return nil, uerr("The app definition could not be read.")
	}
	names := make([]string, 0, len(cfg.Services))
	for n := range cfg.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# Written by NexoVirt Box: what makes this clone its own instance.\nservices:\n")
	for _, name := range names {
		s := cfg.Services[name]
		if s.ContainerName == "" && len(s.Ports) == 0 {
			continue
		}
		if !serviceNameRe.MatchString(name) {
			return nil, uerr("The service name %q cannot be cloned.", name)
		}
		fmt.Fprintf(&b, "  %s:\n", name)
		if s.ContainerName != "" {
			fmt.Fprintf(&b, "    container_name: nxbox-%s-%s\n", id, name)
		}
		if len(s.Ports) == 0 {
			continue
		}
		b.WriteString("    ports: !override\n")
		for _, p := range s.Ports {
			n, err := strconv.Atoi(strings.TrimSpace(fmt.Sprint(p.Published)))
			if err != nil || n < 1 || n > 65535 || p.Target < 1 || p.Target > 65535 {
				return nil, uerr("This app publishes a range of ports, which Box cannot move for a clone.")
			}
			if to, ok := ports[n]; ok {
				n = to
			}
			ip := p.HostIP
			if ip == "" { // every address, IPv4 and IPv6, as Docker does without one
				fmt.Fprintf(&b, "      - \"%d:%d/%s\"\n", n, p.Target, proto(p))
				continue
			}
			if net.ParseIP(ip) == nil {
				return nil, uerr("This app publishes a port on an address Box cannot read.")
			}
			if strings.Contains(ip, ":") {
				ip = "[" + ip + "]"
			}
			fmt.Fprintf(&b, "      - \"%s:%d:%d/%s\"\n", ip, n, p.Target, proto(p))
		}
	}
	return []byte(b.String()), nil
}

var cloneNameLine = regexp.MustCompile(`^    container_name: nxbox-[a-z0-9][a-z0-9-]{0,62}-[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)

// checkCloneFile makes sure a clone.yaml (it can come back from a backup) only renames
// containers and moves ports.
func checkCloneFile(dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, cloneFile))
	if os.IsNotExist(err) {
		return nil
	}
	bad := uerr("The clone file of this app is damaged.")
	if err != nil || len(b) > 64<<10 {
		return bad
	}
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		switch {
		case strings.HasPrefix(l, "#"), l == "services:", l == "    ports: !override", bindServiceLine.MatchString(l), bindPortLine.MatchString(l), cloneNameLine.MatchString(l):
		default:
			return bad
		}
	}
	return nil
}

// planClonePorts keeps every published port that is free and not used by another app, and moves
// the others to the next free port above them.
func planClonePorts(published []hostPort, want map[int]int, taken func(int) bool) (map[int]int, error) {
	out := map[int]int{}
	used := map[int]bool{}
	for _, hp := range published {
		if to, ok := want[hp.port]; ok {
			out[hp.port], used[to] = to, true
		}
	}
	for _, hp := range published {
		if _, ok := out[hp.port]; ok {
			continue
		}
		if !taken(hp.port) && !used[hp.port] {
			out[hp.port], used[hp.port] = hp.port, true
			continue
		}
		next := 0
		for p := hp.port + 1; p <= 65535 && p < hp.port+2000; p++ {
			if !taken(p) && !used[p] {
				next = p
				break
			}
		}
		if next == 0 {
			return nil, uerr("Box found no free port near %d for the clone.", hp.port)
		}
		out[hp.port], used[next] = next, true
	}
	return out, nil
}

// appClone creates and starts the clone.
func (h *helperServer) appClone(ctx context.Context, req helperRequest, logf func(string)) (any, error) {
	if err := validID(req.ID); err != nil {
		return nil, err
	}
	if validID(req.NewID) != nil || req.NewID == "box" || req.NewID == req.ID {
		return nil, uerr("Pick another name for the clone.")
	}
	if err := checkOwnName(req.Name); err != nil {
		return nil, err
	}
	if req.Port != 0 && !validPort(req.Port) {
		return nil, uerr("Pick a port between 1 and 65535.")
	}
	if err := needDocker(); err != nil {
		return nil, err
	}
	src, err := h.readMeta(req.ID)
	if err != nil {
		return nil, err
	}
	release, err := h.claim(req.NewID, "clone")
	if err != nil {
		return nil, err
	}
	defer release()
	dir := h.appDir(req.NewID)
	if exists(dir) {
		return nil, uerr("An app called %s already exists. Pick another name.", req.NewID)
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Minute)
	defer cancel()
	fail := func(err error) (any, error) { os.RemoveAll(dir); return nil, err }
	if err := os.MkdirAll(filepath.Join(dir, "data"), 0o700); err != nil {
		return nil, err
	}
	os.Chmod(filepath.Join(dir, "data"), 0o755)
	srcDir := h.appDir(req.ID)
	for _, f := range []string{"compose.yaml", ".env"} {
		if err := copyPrivate(filepath.Join(srcDir, f), filepath.Join(dir, f)); err != nil {
			return fail(err)
		}
	}
	meta := src
	meta.ID, meta.Name, meta.InstalledAt, meta.Stopped = req.NewID, strings.TrimSpace(req.Name), time.Now().Unix(), false
	meta.DataDir, meta.DataCreated = filepath.Join(dir, "data"), false
	meta.Domain, meta.TLS, meta.DomainPort, meta.Protect = "", "", 0, false
	meta.Limits, meta.LastUpdate, meta.CloneOf = nil, nil, req.ID
	env := map[string]string{"APP_ID": req.NewID, "APP_DATA_DIR": meta.DataDir}
	if req.Port > 0 {
		env["APP_PORT"] = strconv.Itoa(req.Port)
	}
	if err := rewriteEnvKeys(filepath.Join(dir, ".env"), env); err != nil {
		return fail(err)
	}
	if meta.Custom {
		if err := checkCustomRaw(dir); err != nil {
			return fail(err)
		}
	}
	logf("Checking ports and folders for the clone")
	cfg, err := renderConfig(ctx, dir, req.NewID)
	if err != nil {
		return fail(err)
	}
	if err := sharedFolders(cfg, dir, sharedDir(h.base())); err != nil {
		return fail(err)
	}
	published, err := publishedPorts(cfg)
	if err != nil {
		return fail(err)
	}
	want := map[int]int{}
	if req.Port > 0 { // the clone's main port: the old literal main port (or ${APP_PORT}, already rewritten) goes there
		want[src.MainPort], want[req.Port] = req.Port, req.Port
	}
	others := h.publishedByApps()
	plan, err := planClonePorts(published, want, func(p int) bool { return others[p] || !portFree("tcp", p) })
	if err != nil {
		return fail(err)
	}
	b, err := cloneYAML(cfg, req.NewID, plan)
	if err != nil {
		return fail(err)
	}
	if err := os.WriteFile(filepath.Join(dir, cloneFile), b, 0o600); err != nil {
		return fail(err)
	}
	if to, ok := plan[src.MainPort]; ok {
		meta.MainPort = to
	} else if req.Port > 0 {
		meta.MainPort = req.Port
	}
	mb, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "app.json"), mb, 0o600); err != nil {
		return fail(err)
	}
	if err := checkConfig(ctx, dir, req.NewID, h.policy(meta), true); err != nil {
		return fail(err)
	}
	if err := h.up(ctx, meta.Name, dir, req.NewID, logf); err != nil {
		runStream(context.Background(), compose(dir, req.NewID, "down", "--volumes", "--remove-orphans"), func(string) {})
		return fail(err)
	}
	return meta, nil
}

// sharedFolders refuses binds outside the clone's own folder and the shared media folder: the
// clone would write into the original's files.
func sharedFolders(cfgJSON []byte, dir, shared string) error {
	var cfg struct {
		Services map[string]cloneService `json:"services"`
	}
	if err := json.Unmarshal(cfgJSON, &cfg); err != nil {
		return uerr("The app definition could not be read.")
	}
	for _, s := range cfg.Services {
		for _, v := range s.Volumes {
			if v.Type != "bind" {
				continue
			}
			p := filepath.Clean(v.Source)
			if p == dir || within(p, dir) || p == shared || within(p, shared) || filepath.Base(p) == "docker.sock" {
				continue
			}
			return uerr("This app uses the server folder %s, which a clone would share with the original. Box cannot clone it.", p)
		}
	}
	return nil
}

// publishedByApps lists the host ports every installed app's compose file publishes (running or
// not), so a clone never takes the port of a stopped app.
func (h *helperServer) publishedByApps() map[int]bool {
	out := map[int]bool{}
	entries, _ := os.ReadDir(h.appsDir)
	for _, e := range entries {
		if !e.IsDir() || validID(e.Name()) != nil {
			continue
		}
		m, err := h.readMeta(e.Name())
		if err != nil {
			continue
		}
		if m.MainPort > 0 {
			out[m.MainPort] = true
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		cfg, err := renderConfig(ctx, h.appDir(m.ID), m.ID)
		cancel()
		if err != nil {
			continue
		}
		ps, _ := publishedPorts(cfg)
		for _, hp := range ps {
			out[hp.port] = true
		}
	}
	return out
}
