package box

// Web side of the app store: the catalog (verified here for display and again in the helper
// before installs), icon/screenshot proxy, background jobs and the app endpoints.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NexoVirt/nexovirt-box/internal/catalog"
)

// ---- catalog ----

type catalogStore struct {
	mu       sync.RWMutex
	state    string
	url      string
	fallback string // -catalog-url or the built-in default, used while no URL is saved
	cat      *catalog.Catalog
	source   string
	misses   map[string]time.Time // media URLs that failed recently
	client   *http.Client
}

func newCatalogStore(state, url string) *catalogStore {
	url = strings.TrimRight(url, "/")
	c := &catalogStore{state: state, url: url, fallback: url, misses: map[string]time.Time{},
		client: &http.Client{Timeout: 20 * time.Second}}
	if err := c.reload(); err != nil {
		log.Printf("catalog: %v", err)
	}
	return c
}

func (c *catalogStore) getURL() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.url
}

func (c *catalogStore) setURL(u string) {
	c.mu.Lock()
	c.url = strings.TrimRight(u, "/")
	c.mu.Unlock()
}

func (c *catalogStore) reload() error {
	cat, src, err := loadCatalog(c.state)
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.cat, c.source = cat, src
	c.mu.Unlock()
	return nil
}

func (c *catalogStore) get() (*catalog.Catalog, string) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.cat, c.source
}

func (a *App) catalogList(w http.ResponseWriter, r *http.Request) {
	cat, src := a.catalog.get()
	if cat == nil {
		writeError(w, http.StatusInternalServerError, "The app catalog could not be loaded. Check the service log.")
		return
	}
	type listed struct {
		catalog.IndexEntry
		Supported bool `json:"supported"` // installable on this server's architecture
	}
	apps := make([]listed, len(cat.Index.Apps))
	for i, e := range cat.Index.Apps {
		apps[i] = listed{e, archSupported(e.Architectures)}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"source": src, "generated_at": cat.Index.GeneratedAt, "apps": apps, "stacks": cat.Stacks(),
		"arch": goarch(), "sync_url": a.catalog.getURL() != "",
	})
}

func (a *App) catalogApp(w http.ResponseWriter, r *http.Request) {
	cat, _ := a.catalog.get()
	app, err := cat.App(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "This app is not in the catalog.")
		return
	}
	writeJSON(w, http.StatusOK, struct {
		*catalog.App
		Questions []presentedQuestion `json:"questions"`
	}{app, presentQuestions(app.Questions)})
}

// catalogSync downloads a signed catalog from -catalog-url, verifies it and swaps it in.
func (a *App) catalogSync(w http.ResponseWriter, r *http.Request) {
	c := a.catalog
	if c.getURL() == "" {
		cat, src := c.get()
		writeJSON(w, http.StatusOK, map[string]any{"message": fmt.Sprintf("This Box uses its %s catalog with %d apps. No online catalog is set up yet.", src, len(cat.Index.Apps))})
		return
	}
	n, err := c.sync(r.Context())
	if err != nil {
		log.Printf("catalog sync: %v", err)
		var ue userError
		msg := "The catalog could not be downloaded. Try again later."
		if errors.As(err, &ue) {
			msg = ue.msg
		}
		writeError(w, http.StatusBadGateway, msg)
		return
	}
	a.audit(r, "catalog.sync", fmt.Sprintf("%d apps", n))
	writeJSON(w, http.StatusOK, map[string]any{"message": fmt.Sprintf("The catalog is up to date (%d apps).", n)})
}

func (c *catalogStore) fetch(ctx context.Context, name string, limit int64) ([]byte, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", c.getURL()+"/"+name, nil)
	res, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("%s: HTTP %d", name, res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, limit))
}

func (c *catalogStore) sync(ctx context.Context) (int, error) {
	if !strings.HasPrefix(c.getURL(), "https://") {
		return 0, userError{"The catalog URL must use https."}
	}
	tmp := filepath.Join(c.state, "catalog.tmp")
	os.RemoveAll(tmp)
	defer os.RemoveAll(tmp)
	if err := os.MkdirAll(filepath.Join(tmp, "apps"), 0o700); err != nil {
		return 0, err
	}
	for _, f := range []string{"index.json", "index.sig"} {
		b, err := c.fetch(ctx, f, 8<<20)
		if err != nil {
			return 0, err
		}
		os.WriteFile(filepath.Join(tmp, f), b, 0o600)
	}
	cat, err := catalog.Load(os.DirFS(tmp), catalog.PublicKeys)
	if err != nil {
		return 0, userError{"The downloaded catalog is not signed by NexoVirt. It was not used."}
	}
	if cur, _ := c.get(); cur != nil && cat.Index.GeneratedAt < cur.Index.GeneratedAt {
		return 0, userError{"The downloaded catalog is older than the one in use. It was not used."}
	}
	for _, e := range cat.Index.Apps {
		b, err := c.fetch(ctx, e.File, 2<<20)
		if err != nil {
			return 0, err
		}
		if catalog.FileHash(b) != e.SHA256 {
			return 0, userError{"A file of the downloaded catalog does not match its signature. It was not used."}
		}
		os.WriteFile(filepath.Join(tmp, e.File), b, 0o600)
	}
	dst := filepath.Join(c.state, "catalog")
	os.RemoveAll(dst + ".old")
	os.Rename(dst, dst+".old")
	if err := os.Rename(tmp, dst); err != nil {
		os.Rename(dst+".old", dst)
		return 0, err
	}
	os.RemoveAll(dst + ".old")
	return len(cat.Index.Apps), c.reload()
}

// ---- icons and screenshots (fetched by Box so the browser only talks to Box) ----

func (a *App) catalogIcon(w http.ResponseWriter, r *http.Request) {
	cat, _ := a.catalog.get()
	e, ok := cat.Entry(r.PathValue("id"))
	if !ok && a.serveOwnIcon(w, r, r.PathValue("id")) {
		return
	}
	if !ok || e.Icon == "" {
		http.NotFound(w, r)
		return
	}
	a.catalog.serveMedia(w, r, e.Icon)
}

func (a *App) catalogScreenshot(w http.ResponseWriter, r *http.Request) {
	cat, _ := a.catalog.get()
	app, err := cat.App(r.PathValue("id"))
	n, _ := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 0 || n >= len(app.Screenshots) {
		http.NotFound(w, r)
		return
	}
	a.catalog.serveMedia(w, r, app.Screenshots[n])
}

func (c *catalogStore) serveMedia(w http.ResponseWriter, r *http.Request, src string) {
	u, err := url.Parse(src)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") {
		http.NotFound(w, r)
		return
	}
	sum := sha256.Sum256([]byte(src))
	key := hex.EncodeToString(sum[:12])
	path := filepath.Join(c.state, "media", key)
	b, err := os.ReadFile(path)
	if err != nil {
		c.mu.RLock()
		missed, recent := c.misses[key]
		c.mu.RUnlock()
		if recent && time.Since(missed) < 10*time.Minute {
			http.NotFound(w, r)
			return
		}
		b, err = c.download(r.Context(), src)
		if err != nil {
			c.mu.Lock()
			c.misses[key] = time.Now()
			c.mu.Unlock()
			http.NotFound(w, r)
			return
		}
		os.MkdirAll(filepath.Dir(path), 0o700)
		os.WriteFile(path, b, 0o600)
	}
	ct := http.DetectContentType(b)
	if strings.Contains(string(b[:min(len(b), 512)]), "<svg") {
		ct = "image/svg+xml"
	}
	if !strings.HasPrefix(ct, "image/") {
		http.NotFound(w, r)
		return
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("Cache-Control", "private, max-age=86400")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox") // SVGs never run scripts
	w.Write(b)
}

func (c *catalogStore) download(ctx context.Context, src string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", src, nil)
	req.Header.Set("User-Agent", "NexoVirt-Box/"+version)
	res, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, 3<<20))
}

// ---- jobs (long helper actions run in the background, the UI polls) ----
//
// Jobs live in memory while they run and are saved to SQLite (log tail included), so a reload,
// a web restart or a helper restart never leaves an app looking busy forever: on start every
// job that was still running is marked interrupted, and a watchdog stops jobs that made no
// progress for jobStall.

type job struct {
	mu       sync.Mutex
	ID       string    `json:"id"`
	Kind     string    `json:"kind"`
	App      string    `json:"app"`
	Status   string    `json:"status"` // running, done, failed, interrupted
	Lines    []string  `json:"lines"`
	Error    string    `json:"error,omitempty"`
	Result   any       `json:"result,omitempty"`
	Started  int64     `json:"started"`
	Finished int64     `json:"finished,omitempty"`
	progress time.Time // last log line
	saved    time.Time
	cancel   context.CancelFunc
}

type jobs struct {
	mu    sync.Mutex
	m     map[string]*job // running and recently finished
	store *Store
	stall time.Duration
}

const (
	maxJobLines = 400
	jobStall    = 15 * time.Minute
)

func newJobs(store *Store) *jobs {
	js := &jobs{m: map[string]*job{}, store: store, stall: jobStall}
	n, err := store.interruptJobs()
	if err != nil {
		log.Printf("jobs: %v", err)
	} else if n > 0 {
		log.Printf("jobs: %d unfinished job(s) from the last run marked interrupted", n)
	}
	return js
}

const interruptedMsg = "This action was interrupted because NexoVirt Box restarted. Try it again."

// start runs fn in the background. Only one job per app (or for Docker) runs at a time.
func (js *jobs) start(kind, app string, fn func(ctx context.Context, logf func(string)) (any, error)) (*job, error) {
	js.mu.Lock()
	for id, j := range js.m {
		j.mu.Lock()
		if j.Status != "running" && time.Since(time.Unix(j.Finished, 0)) > time.Hour {
			delete(js.m, id)
		} else if j.Status == "running" && j.App == app {
			j.mu.Unlock()
			js.mu.Unlock()
			return nil, userError{"Another action for this app is still running. Wait until it has finished."}
		}
		j.mu.Unlock()
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now()
	j := &job{ID: randomToken()[:16], Kind: kind, App: app, Status: "running", Lines: []string{}, Started: now.Unix(),
		progress: now, cancel: cancel}
	js.m[j.ID] = j
	js.mu.Unlock()
	js.save(j)
	go js.watch(j, ctx)
	go func() {
		defer cancel()
		res, err := fn(ctx, func(s string) {
			j.mu.Lock()
			j.Lines = append(j.Lines, s)
			if len(j.Lines) > maxJobLines {
				j.Lines = j.Lines[len(j.Lines)-maxJobLines:]
			}
			j.progress = time.Now()
			flush := time.Since(j.saved) > 2*time.Second || len(j.Lines) <= 3 // first lines show what it was doing
			j.mu.Unlock()
			if flush {
				js.save(j)
			}
		})
		j.mu.Lock()
		if j.Status == "running" { // the watchdog may have ended it already
			j.Finished = time.Now().Unix()
			if errors.Is(err, errHelperStopped) {
				j.Status, j.Error = "interrupted", friendly(err)
			} else if err != nil {
				j.Status, j.Error = "failed", friendly(err)
			} else {
				j.Status, j.Result = "done", res
			}
		}
		j.mu.Unlock()
		js.save(j)
	}()
	return j, nil
}

// watch fails a job that printed nothing for js.stall and cancels its helper call, which
// stops the action in the helper and frees the app.
func (js *jobs) watch(j *job, ctx context.Context) {
	t := time.NewTicker(min(js.stall/4, 30*time.Second))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			j.mu.Lock()
			stalled := j.Status == "running" && time.Since(j.progress) > js.stall
			if stalled {
				j.Status, j.Finished = "failed", time.Now().Unix()
				j.Error = fmt.Sprintf("Nothing happened for %d minutes, so the action was stopped. Try it again.", int(js.stall.Minutes()))
			}
			j.mu.Unlock()
			if stalled {
				js.save(j)
				j.cancel()
				return
			}
		}
	}
}

func (js *jobs) save(j *job) {
	j.mu.Lock()
	j.saved = time.Now()
	lines, _ := json.Marshal(j.Lines)
	res, _ := json.Marshal(j.Result)
	row := []any{j.ID, j.Kind, j.App, j.Status, j.Started, j.Finished, j.Error, string(lines), string(res)}
	j.mu.Unlock()
	if err := js.store.saveJob(row); err != nil {
		log.Printf("jobs: save %s: %v", row[0], err)
	}
}

// get returns a snapshot of a job from memory or, after a restart, from the database.
func (js *jobs) get(id string) *job {
	js.mu.Lock()
	j := js.m[id]
	js.mu.Unlock()
	if j != nil {
		return j
	}
	return js.store.loadJob(id)
}

// running lists the jobs that are still running (one per app at most).
func (js *jobs) running() []map[string]any {
	js.mu.Lock()
	defer js.mu.Unlock()
	out := []map[string]any{}
	for _, j := range js.m {
		j.mu.Lock()
		if j.Status == "running" {
			out = append(out, map[string]any{"id": j.ID, "kind": j.Kind, "app": j.App, "started": j.Started})
		}
		j.mu.Unlock()
	}
	return out
}

func friendly(err error) string {
	var ue userError
	if errors.As(err, &ue) {
		return ue.msg
	}
	log.Printf("job: %v", err)
	return "The action failed. Check the service log."
}

func (a *App) jobStatus(w http.ResponseWriter, r *http.Request) {
	j := a.jobs.get(r.PathValue("id"))
	if j == nil {
		writeError(w, http.StatusNotFound, "This action is no longer known. Reload the page.")
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	writeJSON(w, http.StatusOK, j)
}

func (a *App) jobsRunning(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.jobs.running())
}

func (a *App) runJob(w http.ResponseWriter, r *http.Request, kind, app, audit string, req helperRequest) {
	uid, _ := a.currentUser(r)
	name, ip := a.store.username(uid), a.clientIP(r)
	j, err := a.jobs.start(kind, app, func(ctx context.Context, logf func(string)) (any, error) {
		raw, err := a.helper.call(ctx, req, logf)
		if err != nil {
			a.store.audit(name, ip, audit+".failed", app)
			return nil, err
		}
		a.store.audit(name, ip, audit, app)
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

// ---- endpoints ----

func (a *App) helperJSON(w http.ResponseWriter, r *http.Request, req helperRequest) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	raw, err := a.helper.call(ctx, req, nil)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, friendly(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(raw)
}

func (a *App) dockerInfo(w http.ResponseWriter, r *http.Request) {
	a.helperJSON(w, r, helperRequest{Action: "docker_info"})
}

func (a *App) dockerInstall(w http.ResponseWriter, r *http.Request) {
	a.runJob(w, r, "docker_install", "docker", "docker.install", helperRequest{Action: "docker_install"})
}

func (a *App) appsList(w http.ResponseWriter, r *http.Request) {
	a.helperJSON(w, r, helperRequest{Action: "apps_status"})
}

type appBody struct {
	Answers           map[string]string `json:"answers"`
	Retry             bool              `json:"retry"`
	AllowDockerSocket bool              `json:"allow_docker_socket"`
	Op                string            `json:"op"`
	DeleteData        bool              `json:"delete_data"`
	DataDir           string            `json:"data_dir"`
	NoCopy            bool              `json:"no_copy"` // update without a rollback copy
}

func readAppBody(r *http.Request) (appBody, error) {
	var b appBody
	r.Body = http.MaxBytesReader(nil, r.Body, 64*1024)
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil && err != io.EOF {
		return b, errors.New("The request could not be read.")
	}
	return b, nil
}

func (a *App) appInstall(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	cat, _ := a.catalog.get()
	if _, ok := cat.Entry(id); !ok {
		writeError(w, http.StatusNotFound, "This app is not in the catalog.")
		return
	}
	b, err := readAppBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	kind := "install"
	if b.Retry {
		kind = "retry"
	}
	a.runJob(w, r, kind, id, "app.install", helperRequest{Action: "app_install", ID: id, Answers: b.Answers, DataDir: b.DataDir, Retry: b.Retry, AllowDockerSocket: b.AllowDockerSocket})
}

func (a *App) appAction(w http.ResponseWriter, r *http.Request) {
	b, err := readAppBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch b.Op {
	case "start", "stop", "restart", "update", "rollback":
	default:
		writeError(w, http.StatusBadRequest, "This action is not allowed.")
		return
	}
	id := r.PathValue("id")
	req := helperRequest{Action: "app_action", ID: id, Op: b.Op}
	if b.Op == "update" && b.NoCopy {
		req.Confirm = "no_copy"
	}
	if b.AllowDockerSocket { // apps installed before Box asked: confirmed once, with the password
		if !a.needProof(w, r) {
			return
		}
		req.AllowDockerSocket = true
		req = a.proofReq(r, req)
		a.audit(r, "app.docker_socket_ok", id)
	}
	a.runJob(w, r, b.Op, id, "app."+b.Op, req)
}

func (a *App) appClone(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name   string
		Port   int
		Expose bool // a protected source's clone published on every address (the UI warns first)
	}
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, newID := r.PathValue("id"), slugID(in.Name)
	if validID(id) != nil || validID(newID) != nil || newID == "box" {
		writeError(w, http.StatusUnprocessableEntity, "Give the clone a name with at least one letter or digit.")
		return
	}
	if cat, _ := a.catalog.get(); cat != nil {
		if _, ok := cat.Entry(newID); ok {
			writeError(w, http.StatusUnprocessableEntity, "An app in the store already uses this name. Pick another name.")
			return
		}
	}
	req := helperRequest{Action: "app_clone", ID: id, NewID: newID, Name: in.Name, Port: in.Port}
	if in.Expose {
		req.Confirm = "expose"
		a.audit(r, "app.clone_expose", id+" -> "+newID)
	}
	a.runJob(w, r, "clone", newID, "app.clone", req)
}

func (a *App) appUninstall(w http.ResponseWriter, r *http.Request) {
	b, err := readAppBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.PathValue("id")
	a.runJob(w, r, "uninstall", id, "app.uninstall", helperRequest{Action: "app_uninstall", ID: id, DeleteData: b.DeleteData})
}

// appCredentials shows the app's own login details (answers and generated passwords).
func (a *App) appCredentials(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !catalog.IDRe.MatchString(id) {
		writeError(w, http.StatusNotFound, "This app does not exist.")
		return
	}
	a.audit(r, "app.credentials", id)
	a.helperJSON(w, r, helperRequest{Action: "app_credentials", ID: id})
}

func (a *App) appLogs(w http.ResponseWriter, r *http.Request) {
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	q := r.URL.Query()
	a.helperJSON(w, r, helperRequest{Action: "app_logs", ID: r.PathValue("id"), Tail: tail, Since: q.Get("since"), Service: q.Get("service")})
}
