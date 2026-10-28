package main

// Notification endpoints and the watchers that raise events: app health and disks (every 30 s),
// firewall roll-backs, updates (after each update check) and sign-ins.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func (a *App) notifyGet(w http.ResponseWriter, r *http.Request) {
	thr, _ := strconv.Atoi(a.store.setting("notify_disk_threshold"))
	if thr == 0 {
		thr = 90
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": notifyEvents, "channels": a.notify.channelsPublic(), "rules": a.notify.rules(),
		"disk_threshold": thr, "history": a.notify.history(50)})
}

func (a *App) notifyChannelSave(w http.ResponseWriter, r *http.Request) {
	var c notifyChannel
	if err := readJSON(r, &c); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if c.Config == nil {
		c.Config = map[string]string{}
	}
	if !a.needProof(w, r) {
		return
	}
	id, err := a.notify.saveChannel(c)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	a.audit(r, "notify.channel_save", c.Kind+" "+c.Name)
	writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}

func (a *App) notifyChannelDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err := a.notify.deleteChannel(id); err != nil {
		writeError(w, http.StatusInternalServerError, "The channel could not be deleted.")
		return
	}
	a.audit(r, "notify.channel_delete", strconv.FormatInt(id, 10))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) notifyChannelTest(w http.ResponseWriter, r *http.Request) {
	if !a.limiter.allow("notifytest:" + a.clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "Too many test messages. Wait a minute and try again.")
		return
	}
	if !a.needProof(w, r) {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	c, err := a.notify.channel(id)
	if err != nil {
		writeError(w, http.StatusNotFound, friendly(err))
		return
	}
	title := "Test message from NexoVirt Box"
	if err := a.notify.deliver(c, "test", title, "If you can read this, the channel "+c.Name+" works."); err != nil {
		a.notify.log("test", title, "", c.Name+": "+friendly(err))
		writeError(w, http.StatusBadGateway, friendly(err))
		return
	}
	a.notify.log("test", title, "", c.Name+": sent")
	a.audit(r, "notify.test", c.Name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) notifyRulesSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Rules         map[string]notifyRule `json:"rules"`
		DiskThreshold int                   `json:"disk_threshold"`
	}
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.DiskThreshold != 0 && (in.DiskThreshold < 50 || in.DiskThreshold > 99) {
		writeError(w, http.StatusUnprocessableEntity, "Pick a disk threshold between 50 and 99 percent.")
		return
	}
	if err := a.notify.setRules(in.Rules); err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	if in.DiskThreshold != 0 {
		a.store.setSetting("notify_disk_threshold", strconv.Itoa(in.DiskThreshold))
	}
	a.audit(r, "notify.rules", "")
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- sign-ins ----

// onLogin raises "new address" for an IP Box has not seen before (not for the very first one).
func (a *App) onLogin(username, ip string) {
	if a.notify == nil || ip == "" {
		return
	}
	var n int
	a.store.db.QueryRow(`SELECT COUNT(*) FROM known_ips`).Scan(&n)
	res, err := a.store.db.Exec(`INSERT OR IGNORE INTO known_ips (ip, first_seen) VALUES (?, ?)`, ip, time.Now().Unix())
	if err != nil {
		return
	}
	if added, _ := res.RowsAffected(); added == 1 && n > 0 {
		a.notify.send(evLoginNewIP, ip, "Sign-in from a new address", fmt.Sprintf("%s signed in from %s, an address Box has not seen before.", username, ip))
	}
}

// onLoginFailed raises a burst alert at 5 failed sign-ins within 10 minutes.
func (a *App) onLoginFailed(ip string) {
	if a.notify == nil {
		return
	}
	var n int
	a.store.db.QueryRow(`SELECT COUNT(*) FROM audit WHERE action = 'login.failed' AND at > ?`, time.Now().Add(-10*time.Minute).Unix()).Scan(&n)
	if n >= 5 {
		a.notify.send(evLoginFailed, "burst", "Many failed sign-ins", fmt.Sprintf("%d wrong sign-ins in the last 10 minutes, the newest from %s.", n, ip))
	}
}

// ---- health watcher ----

type healthState struct {
	down     map[string]int // app id -> consecutive polls seen down
	notified map[string]bool
	disks    map[string]bool
	fwSeen   string
	started  bool
}

func (a *App) watchHealth() {
	hs := &healthState{down: map[string]int{}, notified: map[string]bool{}, disks: map[string]bool{}}
	hs.fwSeen = a.store.setting("notify_fw_seen")
	for {
		time.Sleep(30 * time.Second)
		a.checkApps(hs)
		a.checkDisks(hs)
		a.checkFirewall(hs)
	}
}

func (a *App) checkApps(hs *healthState) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	raw, err := a.helper.call(ctx, helperRequest{Action: "apps_status"}, nil)
	if err != nil {
		return
	}
	var apps []appStatus
	if json.Unmarshal(raw, &apps) != nil {
		return
	}
	running := map[string]string{}
	for _, j := range a.jobs.running() {
		running[fmt.Sprint(j["app"])] = fmt.Sprint(j["kind"])
	}
	for _, ap := range apps {
		busy := ap.Busy != "" || running[ap.ID] != ""
		isDown := !busy && !ap.Stopped && (ap.State == "exited" || ap.State == "restarting" || ap.State == "partial" || ap.State == "stopped")
		switch {
		case busy:
		case isDown:
			hs.down[ap.ID]++
			if hs.down[ap.ID] == 2 && !hs.notified[ap.ID] { // down for two polls in a row
				hs.notified[ap.ID] = true
				msg := "State: " + ap.State + "."
				if ap.ExitCode != 0 {
					msg = fmt.Sprintf("It stopped with exit code %d.", ap.ExitCode)
				}
				if len(ap.LastLines) > 0 {
					msg += "\nLast log lines:\n" + strings.Join(ap.LastLines, "\n")
				}
				a.notify.send(evAppDown, ap.ID+"|"+strconv.FormatInt(time.Now().Unix(), 10), ap.Name+" went down", msg)
			}
		case ap.State == "running":
			if hs.notified[ap.ID] {
				a.notify.send(evAppRecovered, ap.ID+"|"+strconv.FormatInt(time.Now().Unix(), 10), ap.Name+" is running again", "")
			}
			delete(hs.notified, ap.ID)
			delete(hs.down, ap.ID)
		}
	}
}

func (a *App) checkDisks(hs *healthState) {
	thr, _ := strconv.Atoi(a.store.setting("notify_disk_threshold"))
	if thr == 0 {
		thr = 90
	}
	var snap Snapshot
	if json.Unmarshal(a.sampler.get(), &snap) != nil {
		return
	}
	for _, d := range snap.Disks {
		if d.Total == 0 {
			continue
		}
		p := int(d.Used * 100 / d.Total)
		switch {
		case p >= thr && !hs.disks[d.Mountpoint]:
			hs.disks[d.Mountpoint] = true
			a.notify.send(evDiskHigh, d.Mountpoint, fmt.Sprintf("Disk %s is %d %% full", d.Mountpoint, p),
				fmt.Sprintf("%s of %s used on %s.", humanBytes(int64(d.Used)), humanBytes(int64(d.Total)), d.Mountpoint))
		case p < thr-2:
			delete(hs.disks, d.Mountpoint)
		}
	}
}

func (a *App) checkFirewall(hs *healthState) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	raw, err := a.helper.call(ctx, helperRequest{Action: "firewall_status"}, nil)
	if err != nil {
		return
	}
	var st struct {
		LastRollback *struct {
			ID string `json:"id"`
			At int64  `json:"at"`
		} `json:"last_rollback"`
	}
	if json.Unmarshal(raw, &st) != nil || st.LastRollback == nil || st.LastRollback.ID == hs.fwSeen {
		return
	}
	first := hs.fwSeen == "" && !hs.started
	hs.fwSeen, hs.started = st.LastRollback.ID, true
	a.store.setSetting("notify_fw_seen", hs.fwSeen)
	if first && time.Since(time.Unix(st.LastRollback.At, 0)) > 10*time.Minute {
		return // an old roll-back from before notifications were watched
	}
	a.notify.send(evFirewallRolled, st.LastRollback.ID, "A firewall change was rolled back",
		"A firewall change was not confirmed within 60 seconds, so the previous rules were restored at "+time.Unix(st.LastRollback.At, 0).Format("15:04:05")+".")
}

// notifyUpdates is called after every update check.
func (a *App) notifyUpdates() {
	if a.notify == nil {
		return
	}
	a.updates.mu.Lock()
	apps, box := a.updates.apps, a.updates.box
	a.updates.mu.Unlock()
	seen := map[string]bool{}
	json.Unmarshal([]byte(a.store.setting("notify_updates_seen")), &seen)
	next := map[string]bool{}
	var lines []string
	for id, u := range apps {
		if u.Available {
			k := id + ":" + strings.Join(u.Images, ",")
			next[k] = true
			if !seen[k] {
				lines = append(lines, id+": "+strings.Join(u.Images, ", "))
			}
		}
	}
	if box != nil && newerVersion(box.Version, version) {
		k := "box:" + box.Version
		next[k] = true
		if !seen[k] {
			lines = append(lines, "NexoVirt Box "+box.Version)
		}
	}
	b, _ := json.Marshal(next)
	a.store.setSetting("notify_updates_seen", string(b))
	if len(lines) > 0 {
		a.notify.send(evUpdate, strings.Join(lines, "|"), "Updates are available", strings.Join(lines, "\n"))
	}
}

func startNotifier(a *App) {
	n, err := newNotifier(a.store, a.state)
	if err != nil {
		log.Printf("notifications: %v", err)
		return
	}
	a.notify = n
	go a.watchHealth()
}
