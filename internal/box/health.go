package box

// Health overview: disk SMART verdicts (cached from watchDisks), temperatures (hwmon through
// gopsutil, readable without root), a UPS through NUT's upsc when it is installed (helper), and
// the age of the last successful backup per app. watchSystemHealth turns problems into the
// health notification (same key once per 30 minutes, see notify.go).

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v4/sensors"
)

type tempReading struct {
	Sensor   string  `json:"sensor"`
	Celsius  float64 `json:"celsius"`
	High     float64 `json:"high,omitempty"`
	Critical float64 `json:"critical,omitempty"`
}

type upsInfo struct {
	Name    string `json:"name"`
	Model   string `json:"model,omitempty"`
	Status  string `json:"status"` // NUT ups.status, e.g. "OL", "OB DISCHRG", "OB LB"
	Charge  int    `json:"charge"` // percent, -1 unknown
	Runtime int    `json:"runtime"`
	Load    int    `json:"load"` // percent, -1 unknown
}

type backupAge struct {
	App       string `json:"app"`
	LastOK    int64  `json:"last_ok"` // 0 = never
	Scheduled bool   `json:"scheduled"`
}

type healthProblem struct {
	Key   string `json:"key"`
	Level string `json:"level"` // warning | error
	Text  string `json:"text"`
}

const backupStaleDays = 8

var upsNameRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)

// upsStatus asks NUT (upsc) for every configured UPS. Without upsc the answer says so.
func (h *helperServer) upsStatus(req helperRequest, _ func(string)) (any, error) {
	if _, err := exec.LookPath("upsc"); err != nil {
		return map[string]any{"installed": false, "ups": []upsInfo{}}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	list, err := exec.CommandContext(ctx, "upsc", "-l").Output()
	out := []upsInfo{}
	if err != nil {
		return map[string]any{"installed": true, "ups": out, "error": "NUT is installed but does not answer (is upsd running?)."}, nil
	}
	for _, name := range strings.Fields(string(list)) {
		if !upsNameRe.MatchString(name) {
			continue
		}
		b, err := exec.CommandContext(ctx, "upsc", name).Output()
		if err != nil {
			continue
		}
		out = append(out, parseUPS(name, string(b)))
	}
	return map[string]any{"installed": true, "ups": out}, nil
}

func parseUPS(name, s string) upsInfo {
	u := upsInfo{Name: name, Charge: -1, Runtime: -1, Load: -1}
	num := func(v string) int {
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return -1
		}
		return int(f)
	}
	for _, l := range strings.Split(s, "\n") {
		k, v, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		switch strings.TrimSpace(k) {
		case "ups.status":
			u.Status = v
		case "battery.charge":
			u.Charge = num(v)
		case "battery.runtime":
			u.Runtime = num(v)
		case "ups.load":
			u.Load = num(v)
		case "device.model", "ups.model":
			if u.Model == "" {
				u.Model = v
			}
		}
	}
	return u
}

func readTemps(ctx context.Context) []tempReading {
	list, _ := sensors.TemperaturesWithContext(ctx)
	out := []tempReading{}
	for _, t := range list {
		if t.Temperature <= 0 || t.Temperature > 150 {
			continue
		}
		out = append(out, tempReading{Sensor: t.SensorKey, Celsius: t.Temperature, High: t.High, Critical: t.Critical})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Celsius > out[j].Celsius })
	if len(out) > 24 {
		out = out[:24]
	}
	return out
}

func (a *App) backupAges() []backupAge {
	ages := map[string]*backupAge{}
	if rows, err := a.store.db.Query(`SELECT app, MAX(at) FROM backup_history WHERE kind = 'backup' AND status = 'ok' GROUP BY app`); err == nil {
		for rows.Next() {
			var b backupAge
			if rows.Scan(&b.App, &b.LastOK) == nil {
				ages[b.App] = &b
			}
		}
		rows.Close()
	}
	if rows, err := a.store.db.Query(`SELECT DISTINCT app FROM backup_schedules WHERE enabled = 1`); err == nil {
		for rows.Next() {
			var app string
			if rows.Scan(&app) == nil {
				if ages[app] == nil {
					ages[app] = &backupAge{App: app}
				}
				ages[app].Scheduled = true
			}
		}
		rows.Close()
	}
	out := make([]backupAge, 0, len(ages))
	for _, b := range ages {
		out = append(out, *b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].App < out[j].App })
	return out
}

// healthProblems is pure: what in the readings deserves a warning.
func healthProblems(disks []diskInfo, temps []tempReading, ups []upsInfo, backups []backupAge, now time.Time) []healthProblem {
	out := []healthProblem{}
	for _, d := range disks {
		if d.Smart == nil {
			continue
		}
		switch d.Smart.Verdict {
		case "failing":
			out = append(out, healthProblem{"disk:" + d.Path, "error", fmt.Sprintf("Disk %s (%s) is failing. Back up its data and replace it.", d.Path, d.Model)})
		case "warning":
			out = append(out, healthProblem{"disk:" + d.Path, "warning", fmt.Sprintf("Disk %s (%s) reports problems: %s", d.Path, d.Model, strings.Join(d.Smart.Warnings, " "))})
		}
		if t := d.Smart.Temperature; t != nil && *t >= 60 {
			out = append(out, healthProblem{"disktemp:" + d.Path, "warning", fmt.Sprintf("Disk %s is %d °C hot.", d.Path, *t)})
		}
	}
	for _, t := range temps {
		limit := t.Critical
		if limit <= 0 {
			limit = t.High
		}
		if limit > 0 && t.Celsius >= limit-5 {
			out = append(out, healthProblem{"temp:" + t.Sensor, "warning", fmt.Sprintf("%s is at %.0f °C (limit %.0f °C).", t.Sensor, t.Celsius, limit)})
		}
	}
	for _, u := range ups {
		switch {
		case strings.Contains(u.Status, "LB"):
			out = append(out, healthProblem{"ups:" + u.Name, "error", fmt.Sprintf("UPS %s: battery low (%d %%). The server may turn off soon.", u.Name, u.Charge)})
		case strings.Contains(u.Status, "OB"):
			out = append(out, healthProblem{"ups:" + u.Name, "warning", fmt.Sprintf("UPS %s runs on battery (%d %%).", u.Name, u.Charge)})
		case strings.Contains(u.Status, "RB"):
			out = append(out, healthProblem{"ups:" + u.Name, "warning", fmt.Sprintf("UPS %s says its battery needs replacing.", u.Name)})
		}
	}
	for _, b := range backups {
		if !b.Scheduled {
			continue
		}
		name := b.App
		if name == "box" {
			name = "Box settings"
		}
		if b.LastOK == 0 {
			out = append(out, healthProblem{"backup:" + b.App, "warning", name + " has a backup schedule but no successful backup yet."})
		} else if days := int(now.Sub(time.Unix(b.LastOK, 0)).Hours() / 24); days >= backupStaleDays {
			out = append(out, healthProblem{"backup:" + b.App, "warning", fmt.Sprintf("The last successful backup of %s is %d days old.", name, days)})
		}
	}
	return out
}

type healthReport struct {
	Disks        []diskInfo      `json:"disks"`
	DisksChecked int64           `json:"disks_checked"`
	Temps        []tempReading   `json:"temps"`
	UPS          json.RawMessage `json:"ups"`
	Backups      []backupAge     `json:"backups"`
	Problems     []healthProblem `json:"problems"`
}

func (a *App) health(ctx context.Context) healthReport {
	a.disks.mu.Lock()
	disks, at := a.disks.disks, a.disks.at
	a.disks.mu.Unlock()
	if at == 0 {
		if d, err := a.checkDisks(ctx); err == nil {
			disks, at = d, time.Now().Unix()
		}
	}
	rep := healthReport{Disks: disks, DisksChecked: at, Temps: readTemps(ctx), Backups: a.backupAges(), UPS: json.RawMessage(`{"installed":false,"ups":[]}`)}
	if rep.Disks == nil {
		rep.Disks = []diskInfo{}
	}
	var ups struct {
		UPS []upsInfo `json:"ups"`
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	if raw, err := a.helper.call(cctx, helperRequest{Action: "ups_status"}, nil); err == nil {
		rep.UPS = raw
		json.Unmarshal(raw, &ups)
	}
	cancel()
	rep.Problems = healthProblems(disks, rep.Temps, ups.UPS, rep.Backups, time.Now())
	return rep
}

func (a *App) healthGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.health(r.Context()))
}

// watchSystemHealth sends a notification per problem (dedup by key in the notifier).
func (a *App) watchSystemHealth() {
	time.Sleep(5 * time.Minute)
	for {
		if a.notify != nil {
			for _, p := range a.health(context.Background()).Problems {
				if strings.HasPrefix(p.Key, "disk:") {
					continue // SMART changes have their own notification (disk_health)
				}
				key := p.Key
				if strings.HasPrefix(key, "backup:") {
					key += ":" + time.Now().Format("2006-01-02") // once a day, not every 30 minutes
				}
				a.notify.send(evHealth, key, "Health check: "+p.Level, p.Text)
			}
		}
		time.Sleep(10 * time.Minute)
	}
}
