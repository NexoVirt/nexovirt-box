package box

// Network overview: devices in the local network (the kernel's neighbor table, optionally after
// a ping sweep of the box's own /24 only) and which program or app listens on which port.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type lanDevice struct {
	IP    string `json:"ip"`
	MAC   string `json:"mac"`
	Name  string `json:"name,omitempty"`
	State string `json:"state"`
	Iface string `json:"iface"`
}

type listener struct {
	Proto   string `json:"proto"`
	Addr    string `json:"addr"`
	Port    int    `json:"port"`
	Process string `json:"process"`
	App     string `json:"app,omitempty"`
}

// parseNeigh reads `ip -j neigh show`.
func parseNeigh(out []byte) []lanDevice {
	var rows []struct {
		Dst    string   `json:"dst"`
		Dev    string   `json:"dev"`
		Lladdr string   `json:"lladdr"`
		State  []string `json:"state"`
	}
	json.Unmarshal(out, &rows)
	res := []lanDevice{}
	for _, r := range rows {
		ip := net.ParseIP(r.Dst)
		if ip == nil || ip.To4() == nil || r.Lladdr == "" || strings.HasPrefix(r.Dev, "docker") || strings.HasPrefix(r.Dev, "br-") || strings.HasPrefix(r.Dev, "veth") {
			continue
		}
		st := strings.ToLower(strings.Join(r.State, ","))
		if st == "failed" || st == "incomplete" {
			continue
		}
		res = append(res, lanDevice{IP: r.Dst, MAC: r.Lladdr, State: st, Iface: r.Dev})
	}
	return res
}

var ssProcRe = regexp.MustCompile(`users:\(\("([^"]+)"`)

// parseSS reads `ss -ltnupH` (listening TCP and bound UDP sockets with their process).
func parseSS(out string) []listener {
	res := []listener{}
	seen := map[string]bool{}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		f := strings.Fields(l)
		if len(f) < 5 {
			continue
		}
		local := f[4]
		i := strings.LastIndex(local, ":")
		if i < 0 {
			continue
		}
		port, err := strconv.Atoi(local[i+1:])
		if err != nil {
			continue
		}
		addr := strings.Trim(local[:i], "[]")
		if j := strings.Index(addr, "%"); j >= 0 {
			addr = addr[:j]
		}
		proc := ""
		if m := ssProcRe.FindStringSubmatch(l); m != nil {
			proc = m[1]
		}
		key := f[0] + "|" + strconv.Itoa(port) + "|" + proc
		if seen[key] { // the same socket on IPv4 and IPv6
			continue
		}
		seen[key] = true
		res = append(res, listener{Proto: f[0], Addr: addr, Port: port, Process: proc})
	}
	sort.Slice(res, func(i, j int) bool { return res[i].Port < res[j].Port })
	return res
}

var dockerPortRe = regexp.MustCompile(`:(\d+)->\d+/(tcp|udp)`)

// appPorts maps published host ports to Box apps from `docker ps` (project|ports lines).
func appPorts(psOut string) map[string]string {
	m := map[string]string{}
	for _, l := range strings.Split(strings.TrimSpace(psOut), "\n") {
		proj, ports, ok := strings.Cut(l, "|")
		app, mine := strings.CutPrefix(proj, "nxbox-")
		if !ok || !mine {
			continue
		}
		for _, mm := range dockerPortRe.FindAllStringSubmatch(ports, -1) {
			m[mm[2]+"/"+mm[1]] = app
		}
	}
	return m
}

// sweep pings every address of the box's /24 once (32 at a time) so the neighbor table fills.
func sweep(ctx context.Context, ip string) {
	v := net.ParseIP(ip).To4()
	if v == nil {
		return
	}
	sem := make(chan struct{}, 32)
	var wg sync.WaitGroup
	for n := 1; n < 255; n++ {
		target := fmt.Sprintf("%d.%d.%d.%d", v[0], v[1], v[2], n)
		if target == ip {
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			exec.CommandContext(ctx, "ping", "-c", "1", "-W", "1", "-q", target).Run()
		}()
	}
	wg.Wait()
}

func (h *helperServer) networkOverview(req helperRequest, _ func(string)) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ip := serverIP()
	if req.Op == "sweep" {
		sweep(ctx, ip)
	}
	out, _ := exec.CommandContext(ctx, "ip", "-j", "neigh", "show").Output()
	devices := parseNeigh(out)
	var wg sync.WaitGroup
	for i := range devices {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lctx, lcancel := context.WithTimeout(ctx, 2*time.Second)
			defer lcancel()
			if names, err := net.DefaultResolver.LookupAddr(lctx, devices[i].IP); err == nil && len(names) > 0 {
				devices[i].Name = strings.TrimSuffix(names[0], ".")
			}
		}()
	}
	wg.Wait()
	ss, _ := exec.CommandContext(ctx, "ss", "-ltnupH").Output()
	listeners := parseSS(string(ss))
	if needDocker() == nil {
		ps, _ := exec.CommandContext(ctx, "docker", "ps", "--format", `{{.Label "com.docker.compose.project"}}|{{.Ports}}`).Output()
		apps := appPorts(string(ps))
		for i, l := range listeners {
			listeners[i].App = apps[l.Proto+"/"+strconv.Itoa(l.Port)]
		}
	}
	return map[string]any{"ip": ip, "lan": lanOf(ip), "devices": devices, "listeners": listeners}, nil
}

func (a *App) networkOverviewGet(w http.ResponseWriter, r *http.Request) {
	op := ""
	if r.Method == http.MethodPost { // the ping sweep changes something on the network: POST (CSRF checked)
		op = "sweep"
		a.audit(r, "network.sweep", "")
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	raw, err := a.helper.call(ctx, helperRequest{Action: "network_overview", Op: op}, nil)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, friendly(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}
