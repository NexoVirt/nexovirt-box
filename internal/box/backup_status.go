package box

// Which backup failures still matter, plus dismissing them and clearing old history.

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"
)

// backupProblems returns the failures that are still current (hist newest first). Only the latest
// backup per app and target counts (a later success to the SAME target clears a failure; a success
// to another target does not hide it), and failures of deleted apps or targets or dismissed by the
// admin are not current. apps == nil means
// the installed apps are unknown (helper down): then no failure is hidden for that reason.
func backupProblems(hist []backupRecord, apps, targets map[string]bool) []backupRecord {
	out := []backupRecord{}
	seen := map[string]bool{}
	for _, r := range hist {
		if r.Kind != "backup" {
			continue
		}
		pair := r.App + "\x00" + r.Target
		latest := !seen[pair]
		seen[pair] = true
		if r.Status == "ok" {
			continue
		}
		if !latest || r.Dismissed || !targets[r.Target] || (apps != nil && !apps[r.App]) {
			continue
		}
		out = append(out, r)
	}
	return out
}

func (a *App) currentBackupProblems(r *http.Request, targetsRaw json.RawMessage) []backupRecord {
	var t struct{ Targets []backupTarget }
	json.Unmarshal(targetsRaw, &t)
	targets := map[string]bool{}
	for _, x := range t.Targets {
		targets[x.ID] = true
	}
	var apps map[string]bool
	if raw, err := a.helperCall(r, helperRequest{Action: "apps_status"}, 10*time.Second); err == nil {
		var list []appStatus
		if json.Unmarshal(raw, &list) == nil {
			apps = map[string]bool{"box": true}
			for _, ap := range list {
				apps[ap.ID] = true
			}
		}
	}
	return backupProblems(a.store.backupHistory("", 1000), apps, targets)
}

func (a *App) backupHistoryDismiss(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "This entry was not found.")
		return
	}
	res, err := a.store.db.Exec(`UPDATE backup_history SET dismissed = 1 WHERE id = ? AND status = 'failed'`, id)
	if n, _ := res.RowsAffected(); err != nil || n == 0 {
		writeError(w, http.StatusNotFound, "This entry was not found.")
		return
	}
	a.audit(r, "backup.dismiss", strconv.FormatInt(id, 10))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// backupHistoryClear deletes history entries older than older_than_days (0 = all). Entries are
// written when a run finishes, so nothing running is touched.
func (a *App) backupHistoryClear(w http.ResponseWriter, r *http.Request) {
	var in struct {
		OlderThanDays int `json:"older_than_days"`
	}
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.OlderThanDays < 0 || in.OlderThanDays > 3650 {
		writeError(w, http.StatusUnprocessableEntity, "Pick how old the entries must be.")
		return
	}
	if !a.needProof(w, r) {
		return
	}
	cutoff := time.Now().Unix() + 1
	if in.OlderThanDays > 0 {
		cutoff = time.Now().Add(-time.Duration(in.OlderThanDays) * 24 * time.Hour).Unix()
	}
	res, err := a.store.db.Exec(`DELETE FROM backup_history WHERE at < ?`, cutoff)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "The history could not be cleared.")
		return
	}
	n, _ := res.RowsAffected()
	scope := "all"
	if in.OlderThanDays > 0 {
		scope = "older than " + strconv.Itoa(in.OlderThanDays) + " days"
	}
	a.audit(r, "backup.history_clear", scope+": "+strconv.FormatInt(n, 10)+" entries")
	writeJSON(w, http.StatusOK, map[string]int64{"deleted": n})
}
