package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"
)

// updateCache keeps the last update check (apps and Box) for the UI badges.
type updateCache struct {
	mu      sync.Mutex
	at      int64
	apps    map[string]appUpdate
	box     *releaseManifest
	boxErr  string
	running bool
}

const updateInterval = 6 * time.Hour

// watchUpdates checks shortly after start and then every few hours.
func (a *App) watchUpdates() {
	time.Sleep(2 * time.Minute)
	for {
		a.refreshUpdates(context.Background())
		time.Sleep(updateInterval)
	}
}

func (a *App) refreshUpdates(ctx context.Context) {
	a.updates.mu.Lock()
	if a.updates.running {
		a.updates.mu.Unlock()
		return
	}
	a.updates.running = true
	a.updates.mu.Unlock()
	defer func() { a.updates.mu.Lock(); a.updates.running = false; a.updates.mu.Unlock() }()

	ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()
	apps := map[string]appUpdate{}
	if raw, err := a.helper.call(ctx, helperRequest{Action: "updates_check"}, nil); err == nil {
		json.Unmarshal(raw, &apps)
	} else {
		log.Printf("update check: %v", err)
	}
	var box *releaseManifest
	boxErr := ""
	if u := a.store.setting("release_url"); u != "" {
		m, err := fetchManifest(ctx, u)
		if err != nil {
			boxErr = friendly(err)
		} else {
			box = m
		}
	}
	a.updates.mu.Lock()
	a.updates.at, a.updates.apps, a.updates.box, a.updates.boxErr = time.Now().Unix(), apps, box, boxErr
	a.updates.mu.Unlock()
}

func (a *App) updatesJSON() map[string]any {
	a.updates.mu.Lock()
	defer a.updates.mu.Unlock()
	box := map[string]any{"current": version, "configured": a.store.setting("release_url") != "", "error": a.updates.boxErr}
	if m := a.updates.box; m != nil {
		box["latest"], box["notes"], box["available"] = m.Version, m.Notes, newerVersion(m.Version, version)
	}
	apps := a.updates.apps
	if apps == nil {
		apps = map[string]appUpdate{}
	}
	return map[string]any{"checked_at": a.updates.at, "checking": a.updates.running, "apps": apps, "box": box}
}

func (a *App) updatesGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.updatesJSON())
}

// updatesCheck runs a check now and answers when it is done.
func (a *App) updatesCheck(w http.ResponseWriter, r *http.Request) {
	a.refreshUpdates(r.Context())
	writeJSON(w, http.StatusOK, a.updatesJSON())
}

func (a *App) boxUpdateApply(w http.ResponseWriter, r *http.Request) {
	u := a.store.setting("release_url")
	if u == "" {
		writeError(w, http.StatusUnprocessableEntity, "Set an update address in Settings first.")
		return
	}
	a.runJob(w, r, "self_update", "box", "box.update", helperRequest{Action: "self_update", URL: u, HTTPPort: a.port})
}

func (a *App) imagesUnused(w http.ResponseWriter, r *http.Request) {
	a.helperJSON(w, r, helperRequest{Action: "images_unused"})
}

func (a *App) imagesPrune(w http.ResponseWriter, r *http.Request) {
	a.runJob(w, r, "prune", "images", "images.prune", helperRequest{Action: "images_prune"})
}

func (a *App) proxyStatus(w http.ResponseWriter, r *http.Request) {
	a.helperJSON(w, r, helperRequest{Action: "proxy_status"})
}

func (a *App) proxySetup(w http.ResponseWriter, r *http.Request) {
	var in struct {
		HTTPPort  int    `json:"http_port"`
		HTTPSPort int    `json:"https_port"`
		Email     string `json:"email"`
	}
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.runJob(w, r, "proxy", "proxy", "proxy.setup", helperRequest{Action: "proxy_setup", HTTPPort: in.HTTPPort, HTTPSPort: in.HTTPSPort, Email: in.Email})
}

func (a *App) proxyRemove(w http.ResponseWriter, r *http.Request) {
	a.runJob(w, r, "proxy", "proxy", "proxy.remove", helperRequest{Action: "proxy_remove"})
}

func (a *App) appDomain(w http.ResponseWriter, r *http.Request) {
	var in struct{ Domain string }
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	raw, err := a.helper.call(ctx, helperRequest{Action: "app_domain", ID: id, Domain: in.Domain}, nil)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	a.audit(r, "app.domain", id+" "+in.Domain)
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}
