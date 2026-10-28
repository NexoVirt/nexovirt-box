package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
)

func newTestApp(t *testing.T) (*App, http.Handler, string) {
	t.Helper()
	dir := t.TempDir()
	store, err := openStore(filepath.Join(dir, "box.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.db.Close() })
	app := &App{store: store, limiter: newLimiter(store, 5, time.Minute), sampler: newSampler(time.Second), helper: &helperClient{socket: startTestHelper(t, dir)}, catalog: newCatalogStore(dir, ""), jobs: newJobs()}
	if err := app.ensureSetupToken(dir); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(app.tokenPath)
	ui := fstest.MapFS{"index.html": {Data: []byte("<html></html>")}}
	return app, app.routes(ui), strings.TrimSpace(string(b))
}

func post(h http.Handler, path, body, cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "http://box.test"+path, strings.NewReader(body))
	req.Header.Set("Origin", "http://box.test")
	req.RemoteAddr = "10.0.0.1:1234"
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func get(h http.Handler, path, cookie string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "http://box.test"+path, nil)
	if cookie != "" {
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: cookie})
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func sessionFrom(rec *httptest.ResponseRecorder) string {
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			return c.Value
		}
	}
	return ""
}

func TestSetupOnlyOnceAndNeedsToken(t *testing.T) {
	_, h, token := newTestApp(t)

	if rec := post(h, "/api/setup", `{"token":"wrong","username":"admin","password":"long-enough-pw"}`, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("wrong token: got %d", rec.Code)
	}
	if rec := post(h, "/api/setup", `{"token":"`+token+`","username":"admin","password":"short"}`, ""); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("short password: got %d", rec.Code)
	}
	rec := post(h, "/api/setup", `{"token":"`+token+`","username":"Admin","password":"long-enough-pw"}`, "")
	if rec.Code != http.StatusOK || sessionFrom(rec) == "" {
		t.Fatalf("setup: got %d %s", rec.Code, rec.Body)
	}
	if rec := post(h, "/api/setup", `{"token":"`+token+`","username":"evil","password":"long-enough-pw"}`, ""); rec.Code != http.StatusConflict {
		t.Fatalf("second setup: got %d", rec.Code)
	}
}

func TestLoginSessionAndLogout(t *testing.T) {
	_, h, token := newTestApp(t)
	post(h, "/api/setup", `{"token":"`+token+`","username":"admin","password":"long-enough-pw"}`, "")

	if rec := get(h, "/api/system", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: got %d", rec.Code)
	}
	if rec := post(h, "/api/login", `{"username":"admin","password":"nope-nope-nope"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad password: got %d", rec.Code)
	}
	rec := post(h, "/api/login", `{"username":"admin","password":"long-enough-pw"}`, "")
	sess := sessionFrom(rec)
	if rec.Code != http.StatusOK || sess == "" {
		t.Fatalf("login: got %d", rec.Code)
	}
	c := rec.Result().Cookies()[0]
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Fatalf("cookie flags: %+v", c)
	}
	var state map[string]any
	json.Unmarshal(get(h, "/api/auth/state", sess).Body.Bytes(), &state)
	if state["logged_in"] != true || state["username"] != "admin" {
		t.Fatalf("state: %v", state)
	}
	post(h, "/api/logout", "", sess)
	if rec := get(h, "/api/system", sess); rec.Code != http.StatusUnauthorized {
		t.Fatalf("after logout: got %d", rec.Code)
	}
}

func TestLoginRateLimit(t *testing.T) {
	_, h, _ := newTestApp(t)
	for i := 0; i < 5; i++ {
		post(h, "/api/login", `{"username":"x","password":"y"}`, "")
	}
	if rec := post(h, "/api/login", `{"username":"x","password":"y"}`, ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("6th attempt: got %d", rec.Code)
	}
}

func TestCrossOriginPostRefused(t *testing.T) {
	_, h, _ := newTestApp(t)
	req := httptest.NewRequest(http.MethodPost, "http://box.test/api/login", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://evil.example")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin: got %d", rec.Code)
	}
}

func TestFilterPartitions(t *testing.T) {
	parts := []disk.PartitionStat{
		{Device: "/dev/sda1", Mountpoint: "/", Fstype: "ext4"},
		{Device: "tmpfs", Mountpoint: "/run", Fstype: "tmpfs"},
		{Device: "/dev/loop3", Mountpoint: "/snap/core/1", Fstype: "squashfs"},
		{Device: "/dev/sda1", Mountpoint: "/var/www", Fstype: "ext4"}, // bind mount repeat
		{Device: "/dev/sdb1", Mountpoint: "/mnt/data", Fstype: "xfs"},
		{Device: "overlay", Mountpoint: "/var/lib/docker/overlay2/x/merged", Fstype: "overlay"},
		{Device: "rpool/data", Mountpoint: "/rpool/data", Fstype: "zfs"},
	}
	got := filterPartitions(parts)
	want := []string{"/", "/mnt/data", "/rpool/data"}
	if len(got) != len(want) {
		t.Fatalf("got %v", got)
	}
	for i, p := range got {
		if p.Mountpoint != want[i] {
			t.Fatalf("got %v want %v", p.Mountpoint, want[i])
		}
	}
}

func TestSnapshotShape(t *testing.T) {
	s := newSampler(time.Second)
	b, err := json.Marshal(s.sample())
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	for _, k := range []string{"time", "cpu", "memory", "load", "uptime", "network", "disks"} {
		if _, ok := m[k]; !ok {
			t.Fatalf("missing %q in %s", k, b)
		}
	}
	mem := m["memory"].(map[string]any)
	if mem["total"].(float64) <= 0 {
		t.Fatalf("memory total: %v", mem)
	}
	if _, ok := m["disks"].([]any); !ok {
		t.Fatalf("disks must be a list: %s", b)
	}
}

func TestHistoryKeepsLastSamples(t *testing.T) {
	s := newSampler(time.Second)
	for i := 0; i < historySize+5; i++ {
		s.push([]byte(`{"n":` + strconv.Itoa(i) + `}`))
	}
	var got []map[string]int
	if err := json.Unmarshal(s.historyJSON(), &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != historySize || got[0]["n"] != 5 || got[len(got)-1]["n"] != historySize+4 {
		t.Fatalf("history: len %d first %v last %v", len(got), got[0], got[len(got)-1])
	}
}
