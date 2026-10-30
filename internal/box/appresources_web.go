package box

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"
)

func (a *App) appStats(w http.ResponseWriter, r *http.Request) {
	a.helperJSON(w, r, helperRequest{Action: "app_stats"})
}

func (a *App) appLimitsSave(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if validID(id) != nil {
		writeError(w, http.StatusNotFound, "This app does not exist.")
		return
	}
	var l appLimits
	if err := readJSON(r, &l); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.runJob(w, r, "limits", id, "app.limits", helperRequest{Action: "app_limits", ID: id, Limits: &l})
}

const memWarnPct = 90

// watchAppResources warns when an app uses most of its memory limit.
func (a *App) watchAppResources() {
	for {
		time.Sleep(2 * time.Minute)
		if a.notify == nil {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		raw, err := a.helper.call(ctx, helperRequest{Action: "app_stats"}, nil)
		cancel()
		if err != nil {
			log.Printf("app stats: %v", err)
			continue
		}
		var m map[string]appUsage
		json.Unmarshal(raw, &m)
		for id, u := range m {
			if msg := memWarning(u); msg != "" {
				a.notify.send(evAppResources, id+"|mem", id+" is near its memory limit", msg)
			}
		}
	}
}

func memWarning(u appUsage) string {
	if u.Limits.MemoryMB == 0 || u.MemLimit == 0 || u.Mem*100 < u.MemLimit*memWarnPct {
		return ""
	}
	return fmt.Sprintf("It uses %s of its %s limit. Raise the limit or check the app.", humanBytes(u.Mem), humanBytes(u.MemLimit))
}
