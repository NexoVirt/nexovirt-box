package box

// Automatic app updates: per app on/off and a daily time window (server time). Every few minutes
// the web process looks at the last update check; an app with an update that is inside its
// window gets the normal update job (rollback copy + health check, appupdate.go), at most once a
// day. The result is audited as user "auto" and sent as the app_update notification.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

type autoUpdate struct {
	On   bool   `json:"on"`
	From string `json:"from"` // HH:MM
	To   string `json:"to"`   // HH:MM, may be before From (window over midnight)
}

const autoUpdateKey = "app_auto_updates"

func (a *App) autoUpdates() map[string]autoUpdate {
	m := map[string]autoUpdate{}
	json.Unmarshal([]byte(a.store.setting(autoUpdateKey)), &m)
	return m
}

func minutes(hhmm string) int {
	return int(hhmm[0]-'0')*600 + int(hhmm[1]-'0')*60 + int(hhmm[3]-'0')*10 + int(hhmm[4]-'0')
}

// inWindow reports whether t lies in [from, to) (wrapping over midnight when to <= from).
func inWindow(t time.Time, from, to string) bool {
	if !hhmmRe.MatchString(from) || !hhmmRe.MatchString(to) {
		return false
	}
	now, f, e := t.Hour()*60+t.Minute(), minutes(from), minutes(to)
	if f < e {
		return now >= f && now < e
	}
	return now >= f || now < e
}

func (a *App) autoUpdatesGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.autoUpdates())
}

func (a *App) autoUpdateSave(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if validID(id) != nil {
		writeError(w, http.StatusNotFound, "This app does not exist.")
		return
	}
	var in autoUpdate
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !hhmmRe.MatchString(in.From) || !hhmmRe.MatchString(in.To) || in.From == in.To {
		writeError(w, http.StatusUnprocessableEntity, "Pick a start and an end time (HH:MM) for the update window.")
		return
	}
	m := a.autoUpdates()
	m[id] = in
	b, _ := json.Marshal(m)
	if err := a.store.setSetting(autoUpdateKey, string(b)); err != nil {
		writeError(w, http.StatusInternalServerError, "The setting could not be saved.")
		return
	}
	state := "off"
	if in.On {
		state = "on " + in.From + "-" + in.To
	}
	a.audit(r, "app.auto_update", id+": "+state)
	writeJSON(w, http.StatusOK, in)
}

var autoUpdateTried sync.Map // app id -> time of the last automatic try

// watchAutoUpdates runs due automatic updates.
func (a *App) watchAutoUpdates() {
	for {
		time.Sleep(5 * time.Minute)
		a.runAutoUpdates(time.Now())
	}
}

func (a *App) runAutoUpdates(now time.Time) {
	a.updates.mu.Lock()
	avail := map[string]bool{}
	for id, u := range a.updates.apps {
		avail[id] = u.Available
	}
	a.updates.mu.Unlock()
	for id, s := range a.autoUpdates() {
		if !s.On || !avail[id] || !inWindow(now, s.From, s.To) {
			continue
		}
		if t, ok := autoUpdateTried.Load(id); ok && now.Sub(t.(time.Time)) < 20*time.Hour {
			continue
		}
		autoUpdateTried.Store(id, now)
		a.startAutoUpdate(id)
	}
}

func (a *App) startAutoUpdate(id string) {
	_, err := a.jobs.start("update", id, func(ctx context.Context, logf func(string)) (any, error) {
		logf("Automatic update in the update window")
		raw, err := a.helper.call(ctx, helperRequest{Action: "app_action", ID: id, Op: "update"}, logf)
		if err != nil {
			a.store.audit("auto", "", "app.update.failed", id)
			if a.notify != nil {
				msg := friendly(err)
				a.notify.send(evAppUpdate, id+"|failed", "Automatic update of "+id+" did not work", msg)
			}
			return nil, err
		}
		a.store.audit("auto", "", "app.update", id)
		a.updates.mu.Lock()
		if u, ok := a.updates.apps[id]; ok {
			u.Available = false
			a.updates.apps[id] = u
		}
		a.updates.mu.Unlock()
		if a.notify != nil {
			a.notify.send(evAppUpdate, id+"|ok", "Automatic update of "+id, strings.TrimSpace(id+" was updated and answers again."))
		}
		var v any
		json.Unmarshal(raw, &v)
		return v, nil
	})
	if err != nil {
		autoUpdateTried.Delete(id) // another action ran; try again in the next round
	}
}
