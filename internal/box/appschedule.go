package box

// Scheduled start and stop per app: for every weekday an optional start time and stop time
// (server time). Every 30 seconds the web process runs the normal start/stop job for times that
// are due (within 10 minutes, so a short restart does not skip one), at most once per day and
// kind. Runs are audited as user "schedule".

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

type daySchedule struct {
	Start string `json:"start"` // HH:MM or ""
	Stop  string `json:"stop"`
}

type appSchedule struct {
	On   bool           `json:"on"`
	Days [7]daySchedule `json:"days"` // Sunday = 0, like time.Weekday
}

const appScheduleKey = "app_schedules"

func (a *App) appSchedules() map[string]appSchedule {
	m := map[string]appSchedule{}
	json.Unmarshal([]byte(a.store.setting(appScheduleKey)), &m)
	return m
}

func (s appSchedule) validate() error {
	for _, d := range s.Days {
		for _, t := range []string{d.Start, d.Stop} {
			if t != "" && !hhmmRe.MatchString(t) {
				return userError{"Use times like 07:30, or leave a field empty."}
			}
		}
		if d.Start != "" && d.Start == d.Stop {
			return userError{"Start and stop cannot be at the same time."}
		}
	}
	return nil
}

// dueActions is pure: which of start/stop is due at now (within grace after its time).
func dueActions(s appSchedule, now time.Time, grace time.Duration) []string {
	if !s.On {
		return nil
	}
	d := s.Days[now.Weekday()]
	var out []string
	for _, k := range []struct{ op, at string }{{"start", d.Start}, {"stop", d.Stop}} {
		if k.at == "" {
			continue
		}
		at := time.Date(now.Year(), now.Month(), now.Day(), int(k.at[0]-'0')*10+int(k.at[1]-'0'), int(k.at[3]-'0')*10+int(k.at[4]-'0'), 0, 0, now.Location())
		if !now.Before(at) && now.Sub(at) < grace {
			out = append(out, k.op)
		}
	}
	return out
}

func (a *App) appSchedulesGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.appSchedules())
}

func (a *App) appScheduleSave(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if validID(id) != nil {
		writeError(w, http.StatusNotFound, "This app does not exist.")
		return
	}
	var in appSchedule
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := in.validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	m := a.appSchedules()
	m[id] = in
	b, _ := json.Marshal(m)
	if err := a.store.setSetting(appScheduleKey, string(b)); err != nil {
		writeError(w, http.StatusInternalServerError, "The schedule could not be saved.")
		return
	}
	state := "off"
	if in.On {
		state = "on"
	}
	a.audit(r, "app.schedule", id+": "+state)
	writeJSON(w, http.StatusOK, in)
}

var scheduleFired sync.Map // "id|op|2006-01-02" -> true

func (a *App) watchAppSchedules() {
	for {
		time.Sleep(30 * time.Second)
		now := time.Now()
		for id, s := range a.appSchedules() {
			for _, op := range dueActions(s, now, 10*time.Minute) {
				key := id + "|" + op + "|" + now.Format("2006-01-02")
				if _, done := scheduleFired.LoadOrStore(key, true); done {
					continue
				}
				a.runScheduled(id, op, key)
			}
		}
	}
}

func (a *App) runScheduled(id, op, key string) {
	_, err := a.jobs.start(op, id, func(ctx context.Context, logf func(string)) (any, error) {
		logf("Scheduled " + op)
		raw, err := a.helper.call(ctx, helperRequest{Action: "app_action", ID: id, Op: op}, logf)
		if err != nil {
			a.store.audit("schedule", "", "app."+op+".failed", id)
			return nil, err
		}
		a.store.audit("schedule", "", "app."+op, id)
		var v any
		json.Unmarshal(raw, &v)
		return v, nil
	})
	if err != nil {
		scheduleFired.Delete(key) // another action runs: try again in the next round
	}
}
