package box

// Local names: box.local and <app>.box.local in the LAN through mDNS. The helper writes a small
// script that runs `avahi-publish -a -R <name> <ip>` per name and a systemd unit for it (only
// Box's own files: /etc/nexovirt-box/localnames.{json,sh} and nexovirt-box-mdns.service). When
// the reverse proxy is on, Caddy also routes http://<app>.box.local to the app (proxy.go).
// Apps behind the Box sign-in get no local route: it would skip the sign-in.

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	localNamesUnit = "/etc/systemd/system/nexovirt-box-mdns.service"
	labelRe        = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
)

type localNamesConfig struct {
	Enabled bool `json:"enabled"`
}

func localNamesOn() bool {
	var c localNamesConfig
	return readJSONFile(fwPath("localnames.json"), &c) && c.Enabled
}

// lanIP is the IPv4 address this server uses towards the outside (no packet is sent).
func lanIP() string {
	c, err := net.Dial("udp4", "192.0.2.1:9")
	if err != nil {
		return ""
	}
	defer c.Close()
	if a, ok := c.LocalAddr().(*net.UDPAddr); ok && a.IP.To4() != nil && !a.IP.IsLoopback() {
		return a.IP.String()
	}
	return ""
}

// localNames lists box.local plus one name per app with a web port (not behind the sign-in).
func (h *helperServer) localNames() []string {
	names := []string{"box.local"}
	for _, r := range h.localRoutes() {
		names = append(names, r.Domain)
	}
	return names
}

func (h *helperServer) localRoutes() []proxyRoute {
	entries, _ := os.ReadDir(h.appsDir)
	var out []proxyRoute
	for _, e := range entries {
		m, err := h.readMeta(e.Name())
		if err != nil || !labelRe.MatchString(m.ID) || m.Protect {
			continue
		}
		port := m.MainPort
		if m.DomainPort > 0 {
			port = m.DomainPort
		}
		if port > 0 {
			out = append(out, proxyRoute{App: m.ID, Domain: m.ID + ".box.local", Port: port})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out
}

// publishScript is the script the unit runs; names and IP are checked here.
func publishScript(names []string, ip string) (string, error) {
	if p := net.ParseIP(ip); p == nil || p.To4() == nil {
		return "", uerr("Box could not find this server's address in the local network.")
	}
	var b strings.Builder
	b.WriteString("#!/bin/sh\n# Written by NexoVirt Box: local names in the LAN (mDNS).\n")
	for _, n := range names {
		for _, l := range strings.Split(strings.TrimSuffix(n, ".local"), ".") {
			if !labelRe.MatchString(l) {
				return "", uerr("The name %s cannot be published.", n)
			}
		}
		b.WriteString("avahi-publish -a -R " + n + " " + ip + " &\n")
	}
	b.WriteString("wait\n")
	return b.String(), nil
}

func hasAvahi() bool {
	_, err := exec.LookPath("avahi-publish")
	if err != nil {
		return false
	}
	_, err = exec.LookPath("avahi-daemon")
	return err == nil
}

func (h *helperServer) localNamesStatus(req helperRequest, _ func(string)) (any, error) {
	_, proxyOn := readProxyConfig()
	return map[string]any{"enabled": localNamesOn(), "avahi": hasAvahi(), "running": unitActive("nexovirt-box-mdns"),
		"ip": lanIP(), "names": h.localNames(), "proxy": proxyOn}, nil
}

// localNamesSet turns the local names on or off.
func (h *helperServer) localNamesSet(req helperRequest, logf func(string)) (any, error) {
	on := req.Op == "on"
	if req.Op != "on" && req.Op != "off" {
		return nil, uerr("This action is not allowed.")
	}
	if on && !hasAvahi() {
		return nil, uerr("needs_avahi: Box needs Avahi (avahi-daemon and avahi-utils) for local names.")
	}
	if err := writeJSONFile(fwPath("localnames.json"), localNamesConfig{Enabled: on}); err != nil {
		return nil, err
	}
	if err := h.syncLocalNames(logf); err != nil {
		return nil, err
	}
	return h.localNamesStatus(req, logf)
}

// syncLocalNames brings the unit and the Caddy routes in line with the installed apps. It runs
// after installs and uninstalls too (best effort there).
func (h *helperServer) syncLocalNames(logf func(string)) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	script := fwPath("localnames.sh")
	if !localNamesOn() {
		if exists(localNamesUnit) {
			exec.CommandContext(ctx, "systemctl", "disable", "--now", "nexovirt-box-mdns").Run()
			os.Remove(localNamesUnit)
			exec.CommandContext(ctx, "systemctl", "daemon-reload").Run()
		}
		os.Remove(script)
		return h.reloadProxy(logf)
	}
	body, err := publishScript(h.localNames(), lanIP())
	if err != nil {
		return err
	}
	old, _ := os.ReadFile(script)
	os.MkdirAll(filepath.Dir(script), 0o755)
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		return err
	}
	unit := "[Unit]\nDescription=NexoVirt Box local names (mDNS)\nAfter=avahi-daemon.service network-online.target\nRequires=avahi-daemon.service\n\n" +
		"[Service]\nExecStart=/bin/sh " + script + "\nRestart=always\nRestartSec=10\n\n[Install]\nWantedBy=multi-user.target\n"
	oldUnit, _ := os.ReadFile(localNamesUnit)
	if string(oldUnit) != unit {
		if err := os.WriteFile(localNamesUnit, []byte(unit), 0o644); err != nil {
			return err
		}
		exec.CommandContext(ctx, "systemctl", "daemon-reload").Run()
	}
	exec.CommandContext(ctx, "systemctl", "enable", "--now", "avahi-daemon").Run()
	exec.CommandContext(ctx, "systemctl", "enable", "nexovirt-box-mdns").Run()
	if string(old) != body || string(oldUnit) != unit || !unitActive("nexovirt-box-mdns") {
		if err := exec.CommandContext(ctx, "systemctl", "restart", "nexovirt-box-mdns").Run(); err != nil {
			return uerr("The local names service did not start. Check: journalctl -u nexovirt-box-mdns")
		}
	}
	return h.reloadProxy(logf)
}

func unitActive(u string) bool {
	return exec.Command("systemctl", "is-active", "--quiet", u).Run() == nil
}

// refreshLocalNames re-publishes the names after an app came or went (best effort).
func (h *helperServer) refreshLocalNames() {
	if localNamesOn() {
		h.syncLocalNames(func(string) {})
	}
}

// ---- web ----

func (a *App) localNamesGet(w http.ResponseWriter, r *http.Request) {
	a.helperJSON(w, r, helperRequest{Action: "local_names_status"})
}

func (a *App) localNamesSave(w http.ResponseWriter, r *http.Request) {
	var in struct{ On bool }
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	op := "off"
	if in.On {
		op = "on"
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	raw, err := a.helper.call(ctx, helperRequest{Action: "local_names_set", Op: op}, nil)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	a.audit(r, "localnames."+op, "")
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}
