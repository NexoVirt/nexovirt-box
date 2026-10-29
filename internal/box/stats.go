package box

import (
	"encoding/json"
	"fmt"
	"net/http"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	"github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/sensors"
)

type Snapshot struct {
	Time    int64      `json:"time"`
	CPU     CPUStats   `json:"cpu"`
	Memory  MemStats   `json:"memory"`
	Load    [3]float64 `json:"load"`
	Uptime  uint64     `json:"uptime"`
	Network []NetRate  `json:"network"`
	Disks   []Disk     `json:"disks"`
	Temp    float64    `json:"temp,omitempty"` // CPU temperature in °C, 0 when the server has no sensor
}

type CPUStats struct {
	Total float64   `json:"total"`
	Cores []float64 `json:"cores"`
}

type MemStats struct {
	Total     uint64 `json:"total"`
	Used      uint64 `json:"used"`
	SwapTotal uint64 `json:"swap_total"`
	SwapUsed  uint64 `json:"swap_used"`
}

type NetRate struct {
	Name   string  `json:"name"`
	RxRate float64 `json:"rx_rate"` // bytes per second
	TxRate float64 `json:"tx_rate"`
}

type Disk struct {
	Device     string `json:"device"`
	Mountpoint string `json:"mountpoint"`
	FSType     string `json:"fstype"`
	Total      uint64 `json:"total"`
	Used       uint64 `json:"used"`
	Free       uint64 `json:"free"`
}

// historySize keeps 10 minutes at one sample every 2 s, sent first so charts are full on load.
const historySize = 300

// sampler reads the system once per interval and shares the result with every viewer, so
// CPU and network rates are computed from one consistent series.
type sampler struct {
	interval time.Duration
	mu       sync.RWMutex
	latest   []byte
	history  [][]byte // last historySize samples, oldest first
	lastNet  map[string]net.IOCountersStat
	lastAt   time.Time
}

func newSampler(interval time.Duration) *sampler {
	return &sampler{interval: interval, lastNet: map[string]net.IOCountersStat{}}
}

func (s *sampler) run() {
	cpu.Percent(0, true) // prime the CPU counters
	for {
		snap := s.sample()
		b, err := json.Marshal(snap)
		if err == nil {
			s.push(b)
		}
		time.Sleep(s.interval)
	}
}

func (s *sampler) get() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.latest
}

func (s *sampler) push(b []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.latest = b
	s.history = append(s.history, b)
	if len(s.history) > historySize {
		s.history = s.history[len(s.history)-historySize:]
	}
}

// historyJSON returns the kept samples as one JSON array (oldest first).
func (s *sampler) historyJSON() []byte {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := []byte{'['}
	for i, b := range s.history {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, b...)
	}
	return append(out, ']')
}

func (s *sampler) sample() Snapshot {
	snap := Snapshot{Time: time.Now().Unix(), Network: []NetRate{}, Disks: []Disk{}}
	if cores, err := cpu.Percent(0, true); err == nil && len(cores) > 0 {
		var sum float64
		for i := range cores {
			cores[i] = round1(cores[i])
			sum += cores[i]
		}
		snap.CPU = CPUStats{Total: round1(sum / float64(len(cores))), Cores: cores}
	}
	if vm, err := mem.VirtualMemory(); err == nil {
		snap.Memory.Total, snap.Memory.Used = vm.Total, vm.Total-vm.Available
	}
	if sw, err := mem.SwapMemory(); err == nil {
		snap.Memory.SwapTotal, snap.Memory.SwapUsed = sw.Total, sw.Used
	}
	if l, err := load.Avg(); err == nil {
		snap.Load = [3]float64{l.Load1, l.Load5, l.Load15}
	}
	snap.Uptime, _ = host.Uptime()
	snap.Network = s.netRates()
	snap.Temp = cpuTemp()
	if parts, err := disk.Partitions(false); err == nil {
		for _, p := range filterPartitions(parts) {
			u, err := disk.Usage(p.Mountpoint)
			if err != nil || u.Total == 0 {
				continue
			}
			snap.Disks = append(snap.Disks, Disk{p.Device, p.Mountpoint, p.Fstype, u.Total, u.Used, u.Free})
		}
	}
	return snap
}

func (s *sampler) netRates() []NetRate {
	out := []NetRate{}
	counters, err := net.IOCounters(true)
	if err != nil {
		return out
	}
	now := time.Now()
	secs := now.Sub(s.lastAt).Seconds()
	for _, c := range counters {
		if skipInterface(c.Name) {
			continue
		}
		prev, seen := s.lastNet[c.Name]
		r := NetRate{Name: c.Name}
		// A counter that went down (reset, interface re-created) reads as 0 for one sample.
		if seen && secs > 0 && c.BytesRecv >= prev.BytesRecv && c.BytesSent >= prev.BytesSent {
			r.RxRate = float64(c.BytesRecv-prev.BytesRecv) / secs
			r.TxRate = float64(c.BytesSent-prev.BytesSent) / secs
		}
		s.lastNet[c.Name] = c
		out = append(out, r)
	}
	s.lastAt = now
	return out
}

func skipInterface(name string) bool {
	if name == "lo" || strings.HasPrefix(name, "lo0") {
		return true
	}
	for _, p := range []string{"veth", "docker", "br-", "virbr", "tap", "fwbr", "fwln", "fwpr", "utun", "awdl", "llw", "gif", "stf"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

var pseudoFS = map[string]bool{
	"tmpfs": true, "devtmpfs": true, "overlay": true, "squashfs": true, "proc": true, "sysfs": true,
	"cgroup": true, "cgroup2": true, "devpts": true, "mqueue": true, "debugfs": true, "tracefs": true,
	"securityfs": true, "pstore": true, "bpf": true, "autofs": true, "fusectl": true, "configfs": true,
	"hugetlbfs": true, "ramfs": true, "nsfs": true, "efivarfs": true, "binfmt_misc": true,
	"fuse.lxcfs": true, "rpc_pipefs": true, "nfsd": true, "devfs": true, "nullfs": true,
}

// filterPartitions keeps real storage: no pseudo filesystems, no system paths, no snap/loop
// images, and each device once (bind mounts repeat it).
func filterPartitions(parts []disk.PartitionStat) []disk.PartitionStat {
	out := []disk.PartitionStat{}
	seen := map[string]bool{}
	for _, p := range parts {
		if pseudoFS[p.Fstype] || strings.HasPrefix(p.Device, "/dev/loop") {
			continue
		}
		if p.Mountpoint != "/" && hasAnyPrefix(p.Mountpoint, "/proc", "/sys", "/dev", "/run", "/snap", "/var/lib/docker", "/System/Volumes") {
			continue
		}
		if seen[p.Device] {
			continue
		}
		seen[p.Device] = true
		out = append(out, p)
	}
	return out
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if s == p || strings.HasPrefix(s, p+"/") {
			return true
		}
	}
	return false
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }

// ---- handlers ----

func (a *App) system(w http.ResponseWriter, r *http.Request) {
	info := map[string]any{"arch": runtime.GOARCH, "cores": runtime.NumCPU()}
	if h, err := host.Info(); err == nil {
		info["hostname"] = h.Hostname
		info["os"] = strings.TrimSpace(fmt.Sprintf("%s %s", titleCase(h.Platform), h.PlatformVersion))
		info["kernel"] = h.KernelVersion
		info["virtualization"] = h.VirtualizationSystem
	}
	if c, err := cpu.Info(); err == nil && len(c) > 0 {
		info["cpu_model"] = strings.TrimSpace(c[0].ModelName)
	}
	if n, err := cpu.Counts(false); err == nil {
		info["physical_cores"] = n
	}
	writeJSON(w, http.StatusOK, info)
}

func titleCase(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (a *App) statsStream(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, "Live updates are not available.")
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Accel-Buffering", "no")

	send := func() bool {
		b := a.sampler.get()
		if b == nil {
			return true
		}
		if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
			return false
		}
		flusher.Flush()
		return true
	}
	if _, err := fmt.Fprintf(w, "event: history\ndata: %s\n\n", a.sampler.historyJSON()); err != nil {
		return
	}
	send()
	t := time.NewTicker(a.sampler.interval)
	defer t.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-t.C:
			if !send() {
				return
			}
			// End the stream when the session is gone (logout), the browser reconnects and gets 401.
			if _, ok := a.currentUser(r); !ok {
				return
			}
		}
	}
}

// cpuTemp returns the CPU package temperature (or the hottest sensor when no CPU sensor is named).
// Virtual machines usually have none: then 0 and the UI hides the value.
func cpuTemp() float64 {
	list, _ := sensors.SensorsTemperatures() // partial results come with an error, use what is there
	var cpuT, anyT float64
	for _, t := range list {
		if t.Temperature <= 0 || t.Temperature > 150 {
			continue
		}
		k := strings.ToLower(t.SensorKey)
		if hasAnyPrefix(k, "coretemp_package", "k10temp", "zenpower", "cpu", "x86_pkg", "soc") && t.Temperature > cpuT {
			cpuT = t.Temperature
		}
		anyT = max(anyT, t.Temperature)
	}
	if cpuT == 0 {
		cpuT = anyT
	}
	return round1(cpuT)
}
