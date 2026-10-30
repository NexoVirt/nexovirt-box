package box

// Resources per app: live CPU, memory and network from `docker stats`, disk use of the app's
// folders and volumes (measured in the background, at most every 10 minutes), and optional CPU
// and memory limits. Limits live in a Box-written limits.yaml next to compose.yaml that compose()
// adds as a second file, so the admin's compose file stays untouched.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/mem"
	"gopkg.in/yaml.v3"
)

const limitsFile = "limits.yaml"

type appLimits struct {
	CPUs     float64 `json:"cpus,omitempty"`      // 0 = no limit
	MemoryMB int     `json:"memory_mb,omitempty"` // 0 = no limit
}

type appUsage struct {
	CPU      float64   `json:"cpu"`       // percent of one core, summed over the containers
	Mem      int64     `json:"mem"`       // bytes
	MemLimit int64     `json:"mem_limit"` // bytes, the smallest container limit (0 = none set)
	NetRx    int64     `json:"net_rx"`
	NetTx    int64     `json:"net_tx"`
	Disk     int64     `json:"disk"` // bytes, -1 = not measured yet
	Limits   appLimits `json:"limits"`
}

var sizeRe = regexp.MustCompile(`^([0-9.]+)\s*([a-zA-Z]*)$`)

// parseSize reads Docker's human sizes ("12.3MiB", "1.2kB", "0B").
func parseSize(s string) int64 {
	m := sizeRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0
	}
	f, _ := strconv.ParseFloat(m[1], 64)
	mult := map[string]float64{"": 1, "b": 1, "kb": 1e3, "mb": 1e6, "gb": 1e9, "tb": 1e12,
		"kib": 1 << 10, "mib": 1 << 20, "gib": 1 << 30, "tib": 1 << 40}[strings.ToLower(m[2])]
	return int64(f * mult)
}

type statsRow struct {
	ID, CPUPerc, MemUsage, NetIO string
}

// parseStats adds `docker stats --format '{{json .}}'` lines up per app (ids maps container id
// prefixes to app ids).
func parseStats(out []byte, ids map[string]string) map[string]*appUsage {
	res := map[string]*appUsage{}
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		var r statsRow
		if json.Unmarshal([]byte(l), &r) != nil || r.ID == "" {
			continue
		}
		app := ""
		for cid, a := range ids {
			if strings.HasPrefix(cid, r.ID) || strings.HasPrefix(r.ID, cid) {
				app = a
				break
			}
		}
		if app == "" {
			continue
		}
		u := res[app]
		if u == nil {
			u = &appUsage{Disk: -1}
			res[app] = u
		}
		cpu, _ := strconv.ParseFloat(strings.TrimSuffix(r.CPUPerc, "%"), 64)
		u.CPU += cpu
		used, limit, _ := strings.Cut(r.MemUsage, "/")
		u.Mem += parseSize(used)
		if l := parseSize(limit); l > 0 && (u.MemLimit == 0 || l < u.MemLimit) {
			u.MemLimit = l
		}
		rx, tx, _ := strings.Cut(r.NetIO, "/")
		u.NetRx += parseSize(rx)
		u.NetTx += parseSize(tx)
	}
	return res
}

// appDiskCache keeps the measured disk use per app.
var appDiskCache = struct {
	sync.Mutex
	size    map[string]int64
	at      map[string]time.Time
	running map[string]bool
}{size: map[string]int64{}, at: map[string]time.Time{}, running: map[string]bool{}}

func (h *helperServer) appDisk(id string) int64 {
	appDiskCache.Lock()
	defer appDiskCache.Unlock()
	size, ok := appDiskCache.size[id]
	if !ok {
		size = -1
	}
	if !appDiskCache.running[id] && time.Since(appDiskCache.at[id]) > 10*time.Minute {
		appDiskCache.running[id] = true
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			n := treeSize(h.appDir(id))
			if m, err := h.readMeta(id); err == nil && m.DataDir != "" && !within(m.DataDir, h.appDir(id)) {
				n += treeSize(m.DataDir)
			}
			for _, v := range appVolumes(ctx, id) {
				n += treeSize(v.Mount)
			}
			appDiskCache.Lock()
			appDiskCache.size[id], appDiskCache.at[id], appDiskCache.running[id] = n, time.Now(), false
			appDiskCache.Unlock()
		}()
	}
	return size
}

// appStats returns the live usage of every installed app.
func (h *helperServer) appStats(req helperRequest, _ func(string)) (any, error) {
	out := map[string]*appUsage{}
	entries, _ := os.ReadDir(h.appsDir)
	for _, e := range entries {
		if !e.IsDir() || validID(e.Name()) != nil {
			continue
		}
		m, err := h.readMeta(e.Name())
		if err != nil {
			continue
		}
		var l appLimits
		if m.Limits != nil {
			l = *m.Limits
		}
		out[m.ID] = &appUsage{Disk: h.appDisk(m.ID), Limits: l}
	}
	if needDocker() != nil {
		return out, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ps, err := exec.CommandContext(ctx, "docker", "ps", "--no-trunc", "--filter", "label=com.docker.compose.project",
		"--format", `{{.ID}}|{{.Label "com.docker.compose.project"}}`).Output()
	if err != nil {
		return out, nil
	}
	ids := map[string]string{}
	for _, l := range strings.Fields(string(ps)) {
		cid, proj, _ := strings.Cut(l, "|")
		if app, ok := strings.CutPrefix(proj, "nxbox-"); ok && out[app] != nil {
			ids[cid] = app
		}
	}
	if len(ids) == 0 {
		return out, nil
	}
	st, err := exec.CommandContext(ctx, "docker", "stats", "--no-stream", "--format", "{{json .}}").Output()
	if err != nil {
		return out, nil
	}
	for app, u := range parseStats(st, ids) {
		u.Disk, u.Limits = out[app].Disk, out[app].Limits
		out[app] = u
	}
	return out, nil
}

var serviceNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)

// checkLimits validates limits against this server.
func checkLimits(l appLimits, cores int, totalMB int) error {
	if l.CPUs < 0 || (l.CPUs > 0 && (l.CPUs < 0.1 || l.CPUs > float64(cores))) {
		return uerr("Pick a CPU limit between 0.1 and %d cores, or none.", cores)
	}
	if l.MemoryMB < 0 || (l.MemoryMB > 0 && (l.MemoryMB < 32 || l.MemoryMB > totalMB)) {
		return uerr("Pick a memory limit between 32 MB and %d MB, or none.", totalMB)
	}
	return nil
}

// limitsYAML is the second compose file: the same limits for every service of the app.
func limitsYAML(services []string, l appLimits) ([]byte, error) {
	svcs := map[string]map[string]any{}
	for _, s := range services {
		if !serviceNameRe.MatchString(s) {
			return nil, uerr("The service name %q cannot get a limit.", s)
		}
		m := map[string]any{}
		if l.CPUs > 0 {
			m["cpus"] = l.CPUs
		}
		if l.MemoryMB > 0 {
			m["mem_limit"] = fmt.Sprintf("%dm", l.MemoryMB)
		}
		svcs[s] = m
	}
	return yaml.Marshal(map[string]any{"services": svcs})
}

// appLimitsSet writes or removes limits.yaml and recreates the app when it runs.
func (h *helperServer) appLimitsSet(ctx context.Context, req helperRequest, logf func(string)) (any, error) {
	if err := validID(req.ID); err != nil || req.Limits == nil {
		return nil, uerr("This action is not allowed.")
	}
	vm, err := mem.VirtualMemory()
	if err != nil {
		return nil, err
	}
	l := *req.Limits
	if err := checkLimits(l, runtime.NumCPU(), int(vm.Total>>20)); err != nil {
		return nil, err
	}
	if err := needDocker(); err != nil {
		return nil, err
	}
	release, err := h.claim(req.ID, "limits")
	if err != nil {
		return nil, err
	}
	defer release()
	meta, err := h.readMeta(req.ID)
	if err != nil {
		return nil, err
	}
	dir := h.appDir(req.ID)
	path := filepath.Join(dir, limitsFile)
	if meta.Custom { // same gate as every other compose path, before Docker reads anything
		if err := checkCustomRaw(dir); err != nil {
			return nil, err
		}
	}
	prev, prevErr := os.ReadFile(path)
	restore := func() {
		if prevErr == nil {
			os.WriteFile(path, prev, 0o600)
		} else {
			os.Remove(path)
		}
	}
	if l.CPUs == 0 && l.MemoryMB == 0 {
		os.Remove(path)
		meta.Limits = nil
	} else {
		os.Remove(path) // the services come from the compose file alone
		cmd := compose(dir, req.ID, "config", "--services")
		out, err := exec.CommandContext(ctx, cmd.Path, cmd.Args[1:]...).Output()
		if err != nil {
			restore()
			return nil, uerr("The app's services could not be read.")
		}
		b, err := limitsYAML(strings.Fields(string(out)), l)
		if err != nil {
			restore()
			return nil, err
		}
		if err := os.WriteFile(path, b, 0o600); err != nil {
			restore()
			return nil, err
		}
		meta.Limits = &l
	}
	// The full install checks (folders, privileges, the limits file) before anything starts.
	if err := checkConfig(ctx, dir, req.ID, h.policy(meta), false); err != nil {
		restore()
		return nil, err
	}
	if !meta.Stopped {
		logf("Recreating " + meta.Name + " with the new limits")
		if err := runStream(ctx, compose(dir, req.ID, "up", "-d", "--remove-orphans"), logf); err != nil {
			restore()
			bctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			runStream(bctx, compose(dir, req.ID, "up", "-d", "--remove-orphans"), logf)
			cancel()
			return nil, err
		}
	}
	h.writeMeta(meta)
	return map[string]any{"limits": meta.Limits}, nil
}

// checkLimitsFile makes sure a limits.yaml (it can come back from a backup archive) only sets
// cpus and mem_limit, so it cannot add anything else to an app.
func checkLimitsFile(dir string) error {
	b, err := os.ReadFile(filepath.Join(dir, limitsFile))
	if os.IsNotExist(err) {
		return nil
	}
	bad := uerr("The resource limits file of this app is damaged. Set the limits again.")
	if err != nil || len(b) > 64<<10 {
		return bad
	}
	var doc map[string]map[string]map[string]any
	if yaml.Unmarshal(b, &doc) != nil || len(doc) != 1 || doc["services"] == nil {
		return bad
	}
	for name, s := range doc["services"] {
		if !serviceNameRe.MatchString(name) {
			return bad
		}
		for k, v := range s {
			switch k {
			case "cpus":
				if _, ok := v.(float64); !ok {
					if _, ok := v.(int); !ok {
						return bad
					}
				}
			case "mem_limit":
				if str, ok := v.(string); !ok || !memLimitRe.MatchString(str) {
					return bad
				}
			default:
				return bad
			}
		}
	}
	return nil
}

var memLimitRe = regexp.MustCompile(`^[0-9]{1,7}m$`)
