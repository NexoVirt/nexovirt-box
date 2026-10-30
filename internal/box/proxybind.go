package box

// While an app domain has the Box sign-in in front (proxy.go, proxyauth.go), the app's port
// behind that domain is published on 127.0.0.1 only, so nobody can skip the sign-in by opening
// http://<server>:<port>. Box writes bind.yaml next to compose.yaml (a second compose file like
// limits.yaml) that replaces the ports of the services publishing that port (`!override`): the
// protected port bound to loopback, every other port exactly as before. Turning the sign-in off
// removes the file.

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

const bindFile = "bind.yaml"

type portEntry struct {
	HostIP    string `json:"host_ip"`
	Target    int    `json:"target"`
	Published any    `json:"published"`
	Protocol  string `json:"protocol"`
}

// bindYAML builds the override for protecting port (tcp). It reports false when no service
// publishes that port with a single host port (ranges cannot be split safely).
func bindYAML(configJSON []byte, port int) ([]byte, bool, error) {
	var cfg struct {
		Services map[string]struct {
			Ports []portEntry `json:"ports"`
		} `json:"services"`
	}
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return nil, false, uerr("The app definition could not be read.")
	}
	names := make([]string, 0, len(cfg.Services))
	for n := range cfg.Services {
		names = append(names, n)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString("# Written by NexoVirt Box: this port only for Box's sign-in proxy.\nservices:\n")
	found := false
	for _, name := range names {
		ports := cfg.Services[name].Ports
		hit := false
		for _, p := range ports {
			if pub, _ := strconv.Atoi(strings.TrimSpace(fmt.Sprint(p.Published))); pub == port && proto(p) == "tcp" {
				hit = true
			}
		}
		if !hit {
			continue
		}
		if !serviceNameRe.MatchString(name) {
			return nil, false, uerr("The service name %q cannot be changed by Box.", name)
		}
		fmt.Fprintf(&b, "  %s:\n    ports: !override\n", name)
		for _, p := range ports {
			pub := strings.TrimSpace(fmt.Sprint(p.Published))
			n, err := strconv.Atoi(pub)
			if err != nil || n < 1 || n > 65535 || p.Target < 1 || p.Target > 65535 {
				return nil, false, nil // a range or an odd entry: leave this app alone
			}
			ip := p.HostIP
			if n == port && proto(p) == "tcp" {
				ip = "127.0.0.1"
			}
			if ip == "" { // every address, IPv4 and IPv6, as Docker does without one
				fmt.Fprintf(&b, "      - \"%d:%d/%s\"\n", n, p.Target, proto(p))
				continue
			}
			if net.ParseIP(ip) == nil {
				return nil, false, nil
			}
			if strings.Contains(ip, ":") {
				ip = "[" + ip + "]"
			}
			fmt.Fprintf(&b, "      - \"%s:%d:%d/%s\"\n", ip, n, p.Target, proto(p))
		}
		found = true
	}
	return []byte(b.String()), found, nil
}

func proto(p portEntry) string {
	if p.Protocol == "udp" {
		return "udp"
	}
	return "tcp"
}

var (
	bindServiceLine = regexp.MustCompile(`^  [a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}:$`)
	bindPortLine    = regexp.MustCompile(`^      - "(?:(?:\d{1,3}(?:\.\d{1,3}){3}|\[[0-9a-fA-F:.]+\]):)?\d{1,5}:\d{1,5}/(?:tcp|udp)"$`)
)

// checkBindFile makes sure a bind.yaml (it can come back from a backup) only rebinds ports.
func checkBindFile(dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, bindFile))
	if os.IsNotExist(err) {
		return nil
	}
	bad := uerr("The port binding file of this app is damaged. Save its domain again.")
	if err != nil || len(b) > 64<<10 {
		return bad
	}
	for _, l := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		switch {
		case strings.HasPrefix(l, "#"), l == "services:", l == "    ports: !override", bindServiceLine.MatchString(l), bindPortLine.MatchString(l):
		default:
			return bad
		}
	}
	return nil
}

// syncBindFile writes bind.yaml for a protected port (0 = remove it). It renders the compose
// files without the old binding first. When the port cannot be bound to loopback it fails and
// leaves the old file in place, so the caller refuses to recreate the app (fail closed).
func syncBindFile(ctx context.Context, dir, id string, port int) error {
	path := filepath.Join(dir, bindFile)
	if port < 0 { // protected, but no port to keep private: change nothing, refuse (fail closed)
		return uerr("This app is behind the Box sign-in but Box does not know which port to keep private. Turn the sign-in off for its domain and on again.")
	}
	if port == 0 {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	prev, prevErr := os.ReadFile(path)
	os.Remove(path)
	putBack := func() {
		if prevErr == nil {
			os.WriteFile(path, prev, 0o600)
		}
	}
	cfg, err := renderConfig(ctx, dir, id)
	if err != nil {
		putBack()
		return err
	}
	b, ok, err := bindYAML(cfg, port)
	if err != nil || !ok {
		putBack()
		if err == nil {
			err = uerr("Port %d is behind the Box sign-in, but the app no longer publishes it as a single port, so it cannot be kept private. Turn the sign-in off for its domain first, or keep the port.", port)
		}
		return err
	}
	if err := os.WriteFile(path, b, 0o600); err != nil {
		putBack()
		return err
	}
	return nil
}

// applyBinding brings the binding in line with meta (the sign-in on or off) through the install
// checks and recreates a running app. Any failure puts the previous file back.
func (h *helperServer) applyBinding(meta installedApp, logf func(string)) error {
	dir := h.appDir(meta.ID)
	path := filepath.Join(dir, bindFile)
	prev, prevErr := os.ReadFile(path)
	restore := func() {
		if prevErr == nil {
			os.WriteFile(path, prev, 0o600)
		} else {
			os.Remove(path)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := checkConfig(ctx, dir, meta.ID, h.policy(meta), false); err != nil {
		restore()
		return err
	}
	if string(prev) == readOr(path) || meta.Stopped {
		return nil
	}
	logf("Recreating " + meta.Name + " with the new port binding")
	if err := runStream(ctx, compose(dir, meta.ID, "up", "-d", "--remove-orphans"), logf); err != nil {
		restore()
		bctx, bcancel := context.WithTimeout(context.Background(), 5*time.Minute)
		runStream(bctx, compose(dir, meta.ID, "up", "-d", "--remove-orphans"), logf)
		bcancel()
		return err
	}
	return nil
}

func readOr(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}
