package main

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

	"github.com/nexovirt/box/internal/catalog"
)

// ---- catalog ----

type catalogStore struct {
	mu     sync.RWMutex
	state  string
	url    string
	cat    *catalog.Catalog
	source string
	misses map[string]time.Time // media URLs that failed recently
	client *http.Client
}

func newCatalogStore(state, url string) *catalogStore {
	c := &catalogStore{state: state, url: strings.TrimRight(url, "/"), misses: map[string]time.Time{},
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
	writeJSON(w, http.StatusOK, map[string]any{
		"source": src, "generated_at": cat.Index.GeneratedAt, "apps": cat.Index.Apps,
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
	writeJSON(w, http.StatusOK, app)
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

type job struct {
	mu       sync.Mutex
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	App      string   `json:"app"`
	Status   string   `json:"status"` // running, done, failed
	Lines    []string `json:"lines"`
	Error    string   `json:"error,omitempty"`
	Result   any      `json:"result,omitempty"`
	Started  int64    `json:"started"`
	finished time.Time
}

type jobs struct {
	mu sync.Mutex
	m  map[string]*job
}

func newJobs() *jobs { return &jobs{m: map[string]*job{}} }

const maxJobLines = 400

// start runs fn in the background. Only one job per app (or for Docker) runs at a time.
func (js *jobs) start(kind, app string, fn func(logf func(string)) (any, error)) (*job, error) {
	js.mu.Lock()
	for id, j := range js.m {
		j.mu.Lock()
		if j.Status != "running" && time.Since(j.finished) > time.Hour {
			delete(js.m, id)
		} else if j.Status == "running" && j.App == app {
			j.mu.Unlock()
			js.mu.Unlock()
			return nil, userError{"Another action for this app is still running. Wait until it has finished."}
		}
		j.mu.Unlock()
	}
	j := &job{ID: randomToken()[:16], Kind: kind, App: app, Status: "running", Lines: []string{}, Started: time.Now().Unix()}
	js.m[j.ID] = j
	js.mu.Unlock()
	go func() {
		res, err := fn(func(s string) {
			j.mu.Lock()
			j.Lines = append(j.Lines, s)
			if len(j.Lines) > maxJobLines {
				j.Lines = j.Lines[len(j.Lines)-maxJobLines:]
			}
			j.mu.Unlock()
		})
		j.mu.Lock()
		defer j.mu.Unlock()
		j.finished = time.Now()
		if err != nil {
			j.Status, j.Error = "failed", friendly(err)
			return
		}
		j.Status, j.Result = "done", res
	}()
	return j, nil
}

func (js *jobs) get(id string) *job {
	js.mu.Lock()
	defer js.mu.Unlock()
	return js.m[id]
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

func (a *App) runJob(w http.ResponseWriter, r *http.Request, kind, app, audit string, req helperRequest) {
	uid, _ := a.currentUser(r)
	name, ip := a.store.username(uid), a.clientIP(r)
	j, err := a.jobs.start(kind, app, func(logf func(string)) (any, error) {
		raw, err := a.helper.call(context.Background(), req, logf)
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
	Answers    map[string]string `json:"answers"`
	Op         string            `json:"op"`
	DeleteData bool              `json:"delete_data"`
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
	a.runJob(w, r, "install", id, "app.install", helperRequest{Action: "app_install", ID: id, Answers: b.Answers})
}

func (a *App) appAction(w http.ResponseWriter, r *http.Request) {
	b, err := readAppBody(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	switch b.Op {
	case "start", "stop", "restart", "update":
	default:
		writeError(w, http.StatusBadRequest, "This action is not allowed.")
		return
	}
	id := r.PathValue("id")
	a.runJob(w, r, b.Op, id, "app."+b.Op, helperRequest{Action: "app_action", ID: id, Op: b.Op})
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

func (a *App) appLogs(w http.ResponseWriter, r *http.Request) {
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	a.helperJSON(w, r, helperRequest{Action: "app_logs", ID: r.PathValue("id"), Tail: tail})
}
