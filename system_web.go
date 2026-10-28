package main

// Web side of system updates, disk health and network shares. Every root step is a typed helper
// action; this file only checks the request shape, audits and passes it on.

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// helperReply runs one helper action and writes its JSON answer (422 with friendly text on errors).
func (a *App) helperReply(w http.ResponseWriter, r *http.Request, req helperRequest, timeout time.Duration, audit, detail string) {
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	raw, err := a.helper.call(ctx, req, nil)
	if err != nil {
		if audit != "" {
			a.audit(r, audit+".failed", detail)
		}
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	if audit != "" {
		a.audit(r, audit, detail)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(raw)
}

type sysBody struct {
	Op       string      `json:"op"`
	Packages []string    `json:"packages"`
	Confirm  string      `json:"confirm"`
	Path     string      `json:"path"`
	Name     string      `json:"name"`
	Password string      `json:"password"`
	Share    *sambaShare `json:"share"`
	Export   *nfsExport  `json:"export"`
}

func readSys(w http.ResponseWriter, r *http.Request) (sysBody, bool) {
	var in sysBody
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return in, false
	}
	return in, true
}

// ---- system updates ----

func (a *App) sysUpdates(w http.ResponseWriter, r *http.Request) {
	a.helperReply(w, r, helperRequest{Action: "apt_status"}, time.Minute, "", "")
}

func (a *App) sysUpdatesRefresh(w http.ResponseWriter, r *http.Request) {
	a.helperReply(w, r, helperRequest{Action: "apt_refresh"}, 3*time.Minute, "system.updates_refresh", "")
}

func (a *App) sysUpdatesSimulate(w http.ResponseWriter, r *http.Request) {
	if in, ok := readSys(w, r); ok {
		a.helperReply(w, r, helperRequest{Action: "apt_simulate", Op: in.Op, Packages: in.Packages}, 3*time.Minute, "", "")
	}
}

func (a *App) sysUpdatesStart(w http.ResponseWriter, r *http.Request) {
	if in, ok := readSys(w, r); ok {
		detail := "all"
		if in.Op == "selected" {
			detail = strconv.Itoa(len(in.Packages)) + " packages"
		}
		a.helperReply(w, r, helperRequest{Action: "apt_upgrade_start", Op: in.Op, Packages: in.Packages}, 3*time.Minute, "system.updates_install", detail)
	}
}

func (a *App) sysUpdatesProgress(w http.ResponseWriter, r *http.Request) {
	off, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	a.helperReply(w, r, helperRequest{Action: "apt_upgrade_status", Offset: max(off, 0)}, 30*time.Second, "", "")
}

func (a *App) sysAutoUpdates(w http.ResponseWriter, r *http.Request) {
	if in, ok := readSys(w, r); ok {
		a.runJob(w, r, "auto_updates", "system-auto-updates", "system.auto_updates_"+in.Op, helperRequest{Action: "auto_updates", Op: in.Op})
	}
}

func (a *App) sysReboot(w http.ResponseWriter, r *http.Request) {
	if in, ok := readSys(w, r); ok {
		a.helperReply(w, r, helperRequest{Action: "system_reboot", Confirm: in.Confirm}, 30*time.Second, "system.reboot", "")
	}
}

// pkgInstall installs one package from the helper's fixed list (smartmontools, samba, nfs) as a job.
func (a *App) pkgInstall(w http.ResponseWriter, r *http.Request) {
	if in, ok := readSys(w, r); ok {
		if _, known := installable[in.Op]; !known {
			writeError(w, http.StatusUnprocessableEntity, "Box cannot install this.")
			return
		}
		a.runJob(w, r, "install", "pkg-"+in.Op, "system.install", helperRequest{Action: "pkg_install", Op: in.Op})
	}
}

// ---- disks ----

// diskEvent is raised when a disk's health gets worse (good to warning or failing, warning to failing).
type diskEvent struct {
	Disk     string   `json:"disk"`
	Model    string   `json:"model"`
	Serial   string   `json:"serial"`
	From     string   `json:"from"`
	To       string   `json:"to"`
	Warnings []string `json:"warnings"`
	At       int64    `json:"at"`
}

// diskEventSink receives disk health events.
// TODO(notifications): the notifications feature implements this and registers itself with
// app.subscribeDiskEvents in main, so a failing disk sends a mail/push.
type diskEventSink interface{ DiskHealthChanged(diskEvent) }

type diskCache struct {
	mu      sync.Mutex
	at      int64
	disks   []diskInfo
	verdict map[string]string // serial or path -> last verdict
	sinks   []diskEventSink
	running bool
}

func (a *App) subscribeDiskEvents(s diskEventSink) {
	a.disks.mu.Lock()
	a.disks.sinks = append(a.disks.sinks, s)
	a.disks.mu.Unlock()
}

const diskInterval = 6 * time.Hour

func (a *App) watchDisks() {
	time.Sleep(3 * time.Minute)
	for {
		a.checkDisks(context.Background())
		time.Sleep(diskInterval)
	}
}

var verdictRank = map[string]int{"unknown": 0, "good": 1, "warning": 2, "failing": 3}

func (a *App) checkDisks(ctx context.Context) ([]diskInfo, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	raw, err := a.helper.call(ctx, helperRequest{Action: "disks_list"}, nil)
	if err != nil {
		return nil, err
	}
	var res struct {
		Disks []diskInfo `json:"disks"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, err
	}
	a.disks.mu.Lock()
	if a.disks.verdict == nil {
		a.disks.verdict = map[string]string{}
	}
	var events []diskEvent
	for _, d := range res.Disks {
		if d.Smart == nil {
			continue
		}
		key := d.Serial
		if key == "" {
			key = d.Path
		}
		prev, seen := a.disks.verdict[key]
		if !seen {
			prev = "good"
		}
		now := d.Smart.Verdict
		if verdictRank[now] > verdictRank[prev] && verdictRank[now] >= verdictRank["warning"] {
			events = append(events, diskEvent{Disk: d.Path, Model: d.Model, Serial: d.Serial, From: prev, To: now,
				Warnings: d.Smart.Warnings, At: time.Now().Unix()})
		}
		if now != "unknown" {
			a.disks.verdict[key] = now
		}
	}
	a.disks.at, a.disks.disks = time.Now().Unix(), res.Disks
	sinks := append([]diskEventSink(nil), a.disks.sinks...)
	a.disks.mu.Unlock()
	for _, ev := range events {
		a.store.audit("", "", "disk.health_"+ev.To, ev.Disk+" "+ev.Model)
		log.Printf("disk health: %s (%s) is now %s", ev.Disk, ev.Model, ev.To)
		for _, s := range sinks {
			s.DiskHealthChanged(ev)
		}
	}
	return res.Disks, nil
}

func (a *App) disksList(w http.ResponseWriter, r *http.Request) {
	disks, err := a.checkDisks(r.Context())
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	_, hasSmart := firstSmart(disks)
	writeJSON(w, http.StatusOK, map[string]any{"disks": disks, "smartctl": hasSmart, "checked_at": time.Now().Unix()})
}

func firstSmart(disks []diskInfo) (diskInfo, bool) {
	for _, d := range disks {
		if d.Smart != nil && d.Smart.Reason != "not_installed" {
			return d, true
		}
	}
	return diskInfo{}, len(disks) == 0
}

// disksSummary is the cached verdict for the dashboard (never runs smartctl itself).
func (a *App) disksSummary(w http.ResponseWriter, r *http.Request) {
	a.disks.mu.Lock()
	defer a.disks.mu.Unlock()
	type warn struct {
		Disk     string   `json:"disk"`
		Model    string   `json:"model"`
		Verdict  string   `json:"verdict"`
		Warnings []string `json:"warnings"`
	}
	out := []warn{}
	for _, d := range a.disks.disks {
		if d.Smart != nil && (d.Smart.Verdict == "warning" || d.Smart.Verdict == "failing") {
			out = append(out, warn{d.Path, d.Model, d.Smart.Verdict, d.Smart.Warnings})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"checked_at": a.disks.at, "warnings": out})
}

func (a *App) diskSelftest(w http.ResponseWriter, r *http.Request) {
	if in, ok := readSys(w, r); ok {
		a.helperReply(w, r, helperRequest{Action: "smart_selftest", Path: in.Path, Op: in.Op}, time.Minute, "disk.selftest", in.Path+" "+in.Op)
	}
}

// ---- shares ----

func (a *App) sharesStatus(w http.ResponseWriter, r *http.Request) {
	a.helperReply(w, r, helperRequest{Action: "shares_status"}, 30*time.Second, "", "")
}

func (a *App) sambaShareSave(w http.ResponseWriter, r *http.Request) {
	if in, ok := readSys(w, r); ok && in.Share != nil {
		a.helperReply(w, r, helperRequest{Action: "samba_share_save", Share: in.Share}, time.Minute, "shares.samba_save", in.Share.Name)
	} else if ok {
		writeError(w, http.StatusBadRequest, "The share is missing.")
	}
}

func (a *App) sambaShareDelete(w http.ResponseWriter, r *http.Request) {
	a.helperReply(w, r, helperRequest{Action: "samba_share_delete", ID: r.PathValue("id")}, time.Minute, "shares.samba_delete", r.PathValue("id"))
}

func (a *App) sambaUserSave(w http.ResponseWriter, r *http.Request) {
	if in, ok := readSys(w, r); ok {
		a.helperReply(w, r, helperRequest{Action: "samba_user_save", Name: in.Name, Password: in.Password}, time.Minute, "shares.user_save", in.Name)
	}
}

func (a *App) sambaUserDelete(w http.ResponseWriter, r *http.Request) {
	a.helperReply(w, r, helperRequest{Action: "samba_user_delete", Name: r.PathValue("name")}, time.Minute, "shares.user_delete", r.PathValue("name"))
}

func (a *App) nfsExportSave(w http.ResponseWriter, r *http.Request) {
	if in, ok := readSys(w, r); ok && in.Export != nil {
		a.helperReply(w, r, helperRequest{Action: "nfs_export_save", Export: in.Export}, time.Minute, "shares.nfs_save", in.Export.Path)
	} else if ok {
		writeError(w, http.StatusBadRequest, "The export is missing.")
	}
}

func (a *App) nfsExportDelete(w http.ResponseWriter, r *http.Request) {
	a.helperReply(w, r, helperRequest{Action: "nfs_export_delete", ID: r.PathValue("id")}, time.Minute, "shares.nfs_delete", r.PathValue("id"))
}

func (a *App) sambaHandover(w http.ResponseWriter, r *http.Request) {
	if in, ok := readSys(w, r); ok {
		audit := ""
		if in.Op == "apply" {
			audit = "shares.handover"
		}
		a.helperReply(w, r, helperRequest{Action: "samba_handover", Path: in.Path, Name: in.Name, Op: in.Op}, 5*time.Minute, audit, in.Path+" -> "+in.Name)
	}
}
