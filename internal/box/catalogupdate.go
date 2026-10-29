package box

// Installed apps and the catalog they came from: the Docker socket confirmation for apps
// installed before Box asked for it, and catalog updates (a newer compose definition for an
// installed app, applied with a rollback).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NexoVirt/nexovirt-box/internal/catalog"
)

// errNeedsSocket starts with a code the UI looks for: it then asks once and sends
// allow_docker_socket with a fresh password proof.
var errNeedsSocket = userError{"needs_docker_socket: This app uses the Docker socket, which means full control of the server. Allow it to start the app."}

func composeSHA(s []byte) string {
	sum := sha256.Sum256(s)
	return hex.EncodeToString(sum[:])
}

// socketGate is checked before an app starts. Apps installed before Box asked for the Docker
// socket have no SocketOK: the admin confirms once (with a fresh password) and it is stored.
// It returns true when meta changed.
func socketGate(meta *installedApp, app *catalog.App, allow bool, proofOK func() bool) (bool, error) {
	if meta.Custom || meta.SocketOK || app == nil || !app.DockerSocket {
		return false, nil
	}
	if !allow {
		return false, errNeedsSocket
	}
	if !proofOK() {
		return false, uerr("Enter your password to allow Docker access.")
	}
	meta.SocketOK = true
	return true, nil
}

// Simplification: one cached catalog for status polls (60 s), the update itself loads it fresh.
var catCache struct {
	sync.Mutex
	at  time.Time
	cat *catalog.Catalog
}

func cachedCatalog(webState string) *catalog.Catalog {
	catCache.Lock()
	defer catCache.Unlock()
	if catCache.cat == nil || time.Since(catCache.at) > time.Minute {
		if c, _, err := loadCatalog(webState); err == nil {
			catCache.cat, catCache.at = c, time.Now()
		}
	}
	return catCache.cat
}

// catalogUpdateAvailable: the catalog's compose differs from the one the app was installed with
// (installs from before Box recorded it compare the compose file on disk).
func catalogUpdateAvailable(meta installedApp, dir string, app *catalog.App) bool {
	if meta.Custom || app == nil {
		return false
	}
	have := meta.ComposeSHA
	if have == "" {
		b, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
		if err != nil {
			return false
		}
		have = composeSHA(b)
	}
	return have != composeSHA([]byte(app.Compose))
}

// mergeEnv adds the keys the new definition asks for and the .env does not have yet. Existing
// lines (answers, generated passwords) are never touched.
func mergeEnv(env []byte, app *catalog.App) ([]byte, []string, error) {
	have := map[string]bool{}
	for _, l := range strings.Split(string(env), "\n") {
		if k, _, ok := strings.Cut(l, "="); ok {
			have[strings.TrimSpace(k)] = true
		}
	}
	out := bytes.TrimRight(env, "\n")
	added := []string{}
	for _, pq := range presentQuestions(app.Questions) {
		q := pq.Question
		if have[q.Key] {
			continue
		}
		if !envKeyRe.MatchString(q.Key) || strings.ContainsAny(q.Default, "'\r\n\x00") {
			return nil, nil, uerr("The app definition is not valid.")
		}
		v := q.Default
		if q.Generate {
			v = generateValue(q)
		}
		if v == "" && q.Required {
			return nil, nil, uerr("The new version asks for %s. Reinstall the app to answer it.", q.Label)
		}
		out = fmt.Appendf(out, "\n%s='%s'", q.Key, v)
		have[q.Key] = true
		added = append(added, q.Key)
	}
	return append(out, '\n'), added, nil
}

// ---- helper ----

func (h *helperServer) catalogApp(id string) (installedApp, *catalog.App, error) {
	meta, err := h.readMeta(id)
	if err != nil {
		return meta, nil, err
	}
	if meta.Custom {
		return meta, nil, uerr("Own apps are not in the catalog.")
	}
	cat, _, err := loadCatalog(h.webState)
	if err != nil {
		return meta, nil, err
	}
	app, err := cat.App(id)
	if err != nil {
		return meta, nil, uerr("%s is no longer in the catalog.", meta.Name)
	}
	return meta, app, nil
}

// appCatalogDiff is the preview: both compose files and the keys that would be added (names
// only, never values).
func (h *helperServer) appCatalogDiff(req helperRequest, _ func(string)) (any, error) {
	if err := validID(req.ID); err != nil {
		return nil, err
	}
	meta, app, err := h.catalogApp(req.ID)
	if err != nil {
		return nil, err
	}
	dir := h.appDir(req.ID)
	old, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
	if err != nil {
		return nil, uerr("This app is not installed.")
	}
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	_, added, err := mergeEnv(env, app)
	if err != nil {
		return nil, err
	}
	return map[string]any{"old": string(old), "new": app.Compose, "new_keys": added,
		"version_old": meta.Version, "version_new": app.Version, "available": catalogUpdateAvailable(meta, dir, app),
		"docker_socket": app.DockerSocket && !meta.SocketOK}, nil
}

// appCatalogUpdate writes the catalog's compose file, adds new keys to .env, checks it and
// redeploys; on any failure the previous files come back (and run again).
func (h *helperServer) appCatalogUpdate(ctx context.Context, req helperRequest, logf func(string)) (any, error) {
	if err := validID(req.ID); err != nil {
		return nil, err
	}
	if !h.consoleAllowed(req) {
		return nil, uerr("Enter your password to update the app definition.")
	}
	release, err := h.claim(req.ID, "edit")
	if err != nil {
		return nil, err
	}
	defer release()
	if err := needDocker(); err != nil {
		return nil, err
	}
	meta, app, err := h.catalogApp(req.ID)
	if err != nil {
		return nil, err
	}
	if _, err := socketGate(&meta, app, req.AllowDockerSocket, func() bool { return true }); err != nil {
		return nil, err
	}
	dir := h.appDir(req.ID)
	composePath, envPath := filepath.Join(dir, "compose.yaml"), filepath.Join(dir, ".env")
	oldCompose, err := os.ReadFile(composePath)
	if err != nil {
		return nil, uerr("This app is not installed.")
	}
	oldEnv, err := os.ReadFile(envPath)
	if err != nil {
		return nil, err
	}
	newEnv, added, err := mergeEnv(oldEnv, app)
	if err != nil {
		return nil, err
	}
	restore := func() {
		os.WriteFile(composePath, oldCompose, 0o600)
		os.WriteFile(envPath, oldEnv, 0o600)
	}
	if err := os.WriteFile(composePath, []byte(app.Compose), 0o600); err != nil {
		return nil, err
	}
	if err := os.WriteFile(envPath, newEnv, 0o600); err != nil {
		restore()
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 40*time.Minute)
	defer cancel()
	if len(added) > 0 {
		logf("Added to the settings: " + strings.Join(added, ", "))
	}
	logf("Checking ports and folders")
	if err := checkConfig(ctx, dir, req.ID, h.policy(meta), false); err != nil {
		restore()
		return nil, err
	}
	if !meta.Stopped {
		logf("Starting " + meta.Name + " with the new definition")
		if err := runStream(ctx, compose(dir, req.ID, "up", "-d", "--remove-orphans"), logf); err != nil {
			logf("That failed. Putting the previous definition back.")
			restore()
			runStream(ctx, compose(dir, req.ID, "up", "-d", "--remove-orphans"), logf)
			return nil, uerr("%s did not start with the new definition, so Box put the previous one back. The log above says why.", meta.Name)
		}
	}
	meta.ComposeSHA, meta.Version = composeSHA([]byte(app.Compose)), app.Version
	h.writeMeta(meta)
	return map[string]any{"added": added, "version": app.Version}, nil
}

// ---- web ----

func (a *App) appCatalogDiff(w http.ResponseWriter, r *http.Request) {
	a.helperJSON(w, r, helperRequest{Action: "app_catalog_diff", ID: r.PathValue("id")})
}

func (a *App) appCatalogUpdate(w http.ResponseWriter, r *http.Request) {
	b, err := readAppBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !a.needProof(w, r) {
		return
	}
	id := r.PathValue("id")
	a.runJob(w, r, "edit", id, "app.catalog_update", a.proofReq(r, helperRequest{Action: "app_catalog_update", ID: id, AllowDockerSocket: b.AllowDockerSocket}))
}
