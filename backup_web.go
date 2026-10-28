package main

// Web side of backups: schedules and history (SQLite), the scheduler, and the endpoints. Keys,
// targets and every file operation stay in the helper.

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"regexp"
	"strconv"
	"sync"
	"time"
)

type backupSchedule struct {
	ID      int64      `json:"id"`
	App     string     `json:"app"` // app id, "box" or "*" (every app and Box)
	Target  string     `json:"target"`
	Freq    string     `json:"freq"` // daily, weekly
	Weekday int        `json:"weekday"`
	Time    string     `json:"time"` // HH:MM, server time
	Keep    keepPolicy `json:"keep"`
	Enabled bool       `json:"enabled"`
	LastRun int64      `json:"last_run"`
	NextRun int64      `json:"next_run,omitempty"` // filled for the UI, not stored
}

type backupRecord struct {
	ID         int64  `json:"id"`
	At         int64  `json:"at"`
	Kind       string `json:"kind"` // backup, restore, verify
	App        string `json:"app"`
	Target     string `json:"target"`
	TargetName string `json:"target_name"`
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	Seconds    int64  `json:"seconds"`
	Status     string `json:"status"` // ok, failed
	Error      string `json:"error"`
	Source     string `json:"source"` // manual, schedule
}

var hhmmRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

func (s backupSchedule) validate() error {
	if s.App != "*" && s.App != "box" && validID(s.App) != nil {
		return uerr("Pick what to back up.")
	}
	if !targetIDRe.MatchString(s.Target) {
		return uerr("Pick a backup target.")
	}
	if s.Freq != "daily" && s.Freq != "weekly" {
		return uerr("Pick daily or weekly.")
	}
	if s.Weekday < 0 || s.Weekday > 6 || !hhmmRe.MatchString(s.Time) {
		return uerr("Pick a time like 03:30.")
	}
	k := s.Keep
	if k.Last < 0 || k.Daily < 0 || k.Weekly < 0 || k.Last > 1000 || k.Daily > 1000 || k.Weekly > 1000 {
		return uerr("Keep between 0 and 1000 backups.")
	}
	return nil
}

// dueAt is the newest planned run at or before now.
func (s backupSchedule) dueAt(now time.Time) time.Time {
	h, _ := strconv.Atoi(s.Time[:2])
	m, _ := strconv.Atoi(s.Time[3:])
	t := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	for t.After(now) || (s.Freq == "weekly" && int(t.Weekday()) != s.Weekday) {
		t = t.AddDate(0, 0, -1)
	}
	return t
}

// nextAt is the first planned run after now.
func (s backupSchedule) nextAt(now time.Time) time.Time {
	step := 1
	if s.Freq == "weekly" {
		step = 7
	}
	return s.dueAt(now).AddDate(0, 0, step)
}

func (st *Store) schedules() []backupSchedule {
	out := []backupSchedule{}
	rows, err := st.db.Query(`SELECT id, app, target, freq, weekday, time, keep, enabled, last_run FROM backup_schedules ORDER BY id`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var s backupSchedule
		var keep string
		if rows.Scan(&s.ID, &s.App, &s.Target, &s.Freq, &s.Weekday, &s.Time, &keep, &s.Enabled, &s.LastRun) == nil {
			json.Unmarshal([]byte(keep), &s.Keep)
			out = append(out, s)
		}
	}
	return out
}

func (st *Store) saveSchedule(s backupSchedule) (int64, error) {
	keep, _ := json.Marshal(s.Keep)
	if s.ID == 0 {
		res, err := st.db.Exec(`INSERT INTO backup_schedules (app, target, freq, weekday, time, keep, enabled, last_run) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			s.App, s.Target, s.Freq, s.Weekday, s.Time, string(keep), s.Enabled, time.Now().Unix()) // no run for times already past today
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	_, err := st.db.Exec(`UPDATE backup_schedules SET app = ?, target = ?, freq = ?, weekday = ?, time = ?, keep = ?, enabled = ? WHERE id = ?`,
		s.App, s.Target, s.Freq, s.Weekday, s.Time, string(keep), s.Enabled, s.ID)
	return s.ID, err
}

func (st *Store) addBackupRecord(r backupRecord) {
	if _, err := st.db.Exec(`INSERT INTO backup_history (at, kind, app, target, target_name, name, size, seconds, status, error, source) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.At, r.Kind, r.App, r.Target, r.TargetName, r.Name, r.Size, r.Seconds, r.Status, r.Error, r.Source); err != nil {
		log.Printf("backup history: %v", err)
	}
	st.db.Exec(`DELETE FROM backup_history WHERE id <= (SELECT MAX(id) - 1000 FROM backup_history)`)
}

func (st *Store) backupHistory(app string, limit int) []backupRecord {
	out := []backupRecord{}
	q, args := `SELECT id, at, kind, app, target, target_name, name, size, seconds, status, error, source FROM backup_history`, []any{}
	if app != "" {
		q += ` WHERE app = ?`
		args = append(args, app)
	}
	rows, err := st.db.Query(q+` ORDER BY id DESC LIMIT ?`, append(args, limit)...)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var r backupRecord
		if rows.Scan(&r.ID, &r.At, &r.Kind, &r.App, &r.Target, &r.TargetName, &r.Name, &r.Size, &r.Seconds, &r.Status, &r.Error, &r.Source) == nil {
			out = append(out, r)
		}
	}
	return out
}

// ---- running ----

func (a *App) targetName(id string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, err := a.helper.call(ctx, helperRequest{Action: "backup_targets"}, nil)
	if err != nil {
		return id
	}
	var out struct{ Targets []backupTarget }
	json.Unmarshal(raw, &out)
	for _, t := range out.Targets {
		if t.ID == id {
			return t.Name
		}
	}
	return id
}

// startBackup runs one backup as a job, records it and notifies.
func (a *App) startBackup(app, target, source, user, ip string) (*job, error) {
	tname := a.targetName(target)
	return a.jobs.start("backup", app, func(ctx context.Context, logf func(string)) (any, error) {
		start := time.Now()
		raw, err := a.helper.call(ctx, helperRequest{Action: "backup_run", ID: app, TargetID: target}, logf)
		rec := backupRecord{At: start.Unix(), Kind: "backup", App: app, Target: target, TargetName: tname, Source: source,
			Seconds: int64(time.Since(start).Seconds())}
		label := app
		if app == "box" {
			label = "Box settings"
		}
		if err != nil {
			rec.Status, rec.Error = "failed", friendly(err)
			a.store.addBackupRecord(rec)
			a.store.audit(user, ip, "backup.failed", app)
			a.notify.send(evBackupFailed, app+"|"+strconv.FormatInt(start.Unix(), 10), "Backup of "+label+" failed", rec.Error)
			return nil, err
		}
		var res struct {
			Name    string `json:"name"`
			Size    int64  `json:"size"`
			Seconds int64  `json:"seconds"`
		}
		json.Unmarshal(raw, &res)
		rec.Status, rec.Name, rec.Size, rec.Seconds = "ok", res.Name, res.Size, res.Seconds
		a.store.addBackupRecord(rec)
		a.store.audit(user, ip, "backup.run", app+" "+res.Name)
		a.notify.send(evBackupOK, res.Name, "Backup of "+label+" saved", fmt.Sprintf("%s, %s, saved to %s.", res.Name, humanBytes(res.Size), tname))
		return res, nil
	})
}

// waitJob blocks until a job is no longer running.
func waitJob(j *job) string {
	for {
		j.mu.Lock()
		st := j.Status
		j.mu.Unlock()
		if st != "running" {
			return st
		}
		time.Sleep(2 * time.Second)
	}
}

var schedMu sync.Mutex // one scheduled run at a time

// watchBackups checks the schedules every 30 seconds.
func (a *App) watchBackups() {
	for {
		time.Sleep(30 * time.Second)
		now := time.Now()
		for _, s := range a.store.schedules() {
			due := s.dueAt(now)
			if !s.Enabled || due.Unix() <= s.LastRun || now.Sub(due) > 24*time.Hour {
				continue
			}
			a.store.db.Exec(`UPDATE backup_schedules SET last_run = ? WHERE id = ?`, now.Unix(), s.ID)
			go a.runSchedule(s)
		}
	}
}

func (a *App) runSchedule(s backupSchedule) {
	schedMu.Lock()
	defer schedMu.Unlock()
	apps := []string{s.App}
	if s.App == "*" {
		apps = []string{"box"}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		raw, err := a.helper.call(ctx, helperRequest{Action: "apps_status"}, nil)
		cancel()
		if err != nil {
			a.notify.send(evBackupFailed, "schedule"+strconv.FormatInt(s.ID, 10), "Scheduled backup could not start", friendly(err))
			return
		}
		var list []appStatus
		json.Unmarshal(raw, &list)
		for _, ap := range list {
			apps = append(apps, ap.ID)
		}
	}
	for _, app := range apps {
		var j *job
		var err error
		for try := 0; try < 30; try++ { // the app may be busy with another action for a while
			if j, err = a.startBackup(app, s.Target, "schedule", "schedule", ""); err == nil {
				break
			}
			time.Sleep(time.Minute)
		}
		if err != nil {
			a.store.addBackupRecord(backupRecord{At: time.Now().Unix(), Kind: "backup", App: app, Target: s.Target, Status: "failed", Error: friendly(err), Source: "schedule"})
			a.notify.send(evBackupFailed, app+"|busy", "Backup of "+app+" was skipped", friendly(err))
			continue
		}
		if waitJob(j) == "done" && (s.Keep != keepPolicy{}) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			raw, err := a.helper.call(ctx, helperRequest{Action: "backup_prune", ID: app, TargetID: s.Target, Keep: s.Keep}, nil)
			cancel()
			if err != nil {
				log.Printf("backup prune %s: %v", app, err)
			} else {
				var out struct{ Deleted []string }
				json.Unmarshal(raw, &out)
				if len(out.Deleted) > 0 {
					a.store.audit("schedule", "", "backup.prune", fmt.Sprintf("%s: %d old backups", app, len(out.Deleted)))
				}
			}
		}
	}
}

// ---- endpoints ----

func (a *App) helperCall(r *http.Request, req helperRequest, timeout time.Duration) (json.RawMessage, error) {
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	return a.helper.call(ctx, req, nil)
}

func (a *App) backupsGet(w http.ResponseWriter, r *http.Request) {
	key, err := a.helperCall(r, helperRequest{Action: "backup_key"}, 10*time.Second)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, friendly(err))
		return
	}
	targets, err := a.helperCall(r, helperRequest{Action: "backup_targets"}, 10*time.Second)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, friendly(err))
		return
	}
	limit := 100
	if n, _ := strconv.Atoi(r.URL.Query().Get("limit")); n > 0 && n <= 500 {
		limit = n
	}
	scheds, now := a.store.schedules(), time.Now()
	for i := range scheds {
		if scheds[i].Enabled {
			scheds[i].NextRun = scheds[i].nextAt(now).Unix()
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "targets": targets, "schedules": scheds,
		"history": a.store.backupHistory(r.URL.Query().Get("app"), limit)})
}

func (a *App) backupKeySet(w http.ResponseWriter, r *http.Request) {
	var in struct{ Op, Key, Confirm string }
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.Op != "create" && in.Op != "import" {
		writeError(w, http.StatusBadRequest, "This action is not allowed.")
		return
	}
	if in.Confirm == "REPLACE" && !a.needProof(w, r) {
		return
	}
	raw, err := a.helperCall(r, helperRequest{Action: "backup_key", Op: in.Op, Key: in.Key, Confirm: in.Confirm}, 10*time.Second)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	a.audit(r, "backup.key_"+in.Op, "")
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(raw)
}

// proofReq adds the session's password proof; the helper checks it itself.
func (a *App) proofReq(r *http.Request, req helperRequest) helperRequest {
	tok := sessionToken(r)
	req.Session, req.Proof = hashToken(tok), a.consoleProof(tok)
	return req
}

func (a *App) backupTargetSave(w http.ResponseWriter, r *http.Request) {
	var t backupTarget
	if err := readJSON(r, &t); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !a.needProof(w, r) {
		return
	}
	raw, err := a.helperCall(r, a.proofReq(r, helperRequest{Action: "backup_target_save", Target: &t}), 15*time.Second)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	a.audit(r, "backup.target_save", t.Name)
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}

func (a *App) backupTargetDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	for _, s := range a.store.schedules() {
		if s.Target == id {
			writeError(w, http.StatusConflict, "A schedule still uses this target. Delete or change the schedule first.")
			return
		}
	}
	if !a.needProof(w, r) {
		return
	}
	if _, err := a.helperCall(r, a.proofReq(r, helperRequest{Action: "backup_target_delete", TargetID: id}), 10*time.Second); err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	a.audit(r, "backup.target_delete", id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) backupArchives(w http.ResponseWriter, r *http.Request) {
	raw, err := a.helperCall(r, helperRequest{Action: "backup_list", TargetID: r.PathValue("id"), ID: r.URL.Query().Get("app")}, 90*time.Second)
	if err != nil {
		writeError(w, http.StatusBadGateway, friendly(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(raw)
}

func (a *App) backupScheduleSave(w http.ResponseWriter, r *http.Request) {
	var s backupSchedule
	if err := readJSON(r, &s); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	id, err := a.store.saveSchedule(s)
	if err != nil {
		log.Printf("schedule: %v", err)
		writeError(w, http.StatusInternalServerError, "The schedule could not be saved.")
		return
	}
	a.audit(r, "backup.schedule_save", fmt.Sprintf("%s %s %s", s.App, s.Freq, s.Time))
	writeJSON(w, http.StatusOK, map[string]int64{"id": id})
}

func (a *App) backupScheduleDelete(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	a.store.db.Exec(`DELETE FROM backup_schedules WHERE id = ?`, id)
	a.audit(r, "backup.schedule_delete", strconv.FormatInt(id, 10))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

type backupBody struct {
	App               string `json:"app"`
	Target            string `json:"target"`
	Name              string `json:"name"`
	NewID             string `json:"new_id"`
	AllowDockerSocket bool   `json:"allow_docker_socket"`
}

func (a *App) backupRunNow(w http.ResponseWriter, r *http.Request) {
	var in backupBody
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.App != "box" && validID(in.App) != nil {
		writeError(w, http.StatusUnprocessableEntity, "Pick what to back up.")
		return
	}
	uid, _ := a.currentUser(r)
	j, err := a.startBackup(in.App, in.Target, "manual", a.store.username(uid), a.clientIP(r))
	if err != nil {
		writeError(w, http.StatusConflict, friendly(err))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job": j.ID})
}

// needProof answers 428 when the session has no password proof from the last minutes.
func (a *App) needProof(w http.ResponseWriter, r *http.Request) bool {
	if a.consoleProof(sessionToken(r)) != "" {
		return true
	}
	writeJSON(w, http.StatusPreconditionRequired, map[string]any{"error": "Enter your password to continue.", "needs_password": true})
	return false
}

func (a *App) backupRestore(w http.ResponseWriter, r *http.Request) {
	var in backupBody
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	app, _, ok := parseArchiveName(in.Name)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "Pick a backup.")
		return
	}
	if !a.needProof(w, r) {
		return
	}
	dest := app
	if in.NewID != "" {
		dest = in.NewID
	}
	tok := sessionToken(r)
	a.backupJob(w, r, "restore", dest, in, helperRequest{Action: "backup_restore", TargetID: in.Target, Name: in.Name, NewID: in.NewID,
		AllowDockerSocket: in.AllowDockerSocket, Session: hashToken(tok), Proof: a.consoleProof(tok)})
}

func (a *App) backupVerify(w http.ResponseWriter, r *http.Request) {
	var in backupBody
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	app, _, ok := parseArchiveName(in.Name)
	if !ok {
		writeError(w, http.StatusUnprocessableEntity, "Pick a backup.")
		return
	}
	a.backupJob(w, r, "verify", app, in, helperRequest{Action: "backup_verify", TargetID: in.Target, Name: in.Name})
}

// backupJob runs a restore or verify as a job and records it in the history.
func (a *App) backupJob(w http.ResponseWriter, r *http.Request, kind, app string, in backupBody, req helperRequest) {
	uid, _ := a.currentUser(r)
	user, ip := a.store.username(uid), a.clientIP(r)
	tname := a.targetName(in.Target)
	j, err := a.jobs.start(kind, app, func(ctx context.Context, logf func(string)) (any, error) {
		start := time.Now()
		raw, err := a.helper.call(ctx, req, logf)
		rec := backupRecord{At: start.Unix(), Kind: kind, App: app, Target: in.Target, TargetName: tname, Name: in.Name,
			Seconds: int64(time.Since(start).Seconds()), Status: "ok", Source: "manual"}
		if err != nil {
			rec.Status, rec.Error = "failed", friendly(err)
		}
		a.store.addBackupRecord(rec)
		a.store.audit(user, ip, "backup."+kind+map[bool]string{true: ".failed", false: ""}[err != nil], app+" "+in.Name)
		if err != nil {
			return nil, err
		}
		var v any
		json.Unmarshal(raw, &v)
		return v, nil
	})
	if err != nil {
		writeError(w, http.StatusConflict, friendly(err))
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]string{"job": j.ID})
}

func (a *App) backupDelete(w http.ResponseWriter, r *http.Request) {
	var in backupBody
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !a.needProof(w, r) {
		return
	}
	if _, err := a.helperCall(r, a.proofReq(r, helperRequest{Action: "backup_delete", TargetID: in.Target, Name: in.Name}), 60*time.Second); err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	a.audit(r, "backup.delete", in.Name)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
