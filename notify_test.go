package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestNotifier(t *testing.T) (*notifier, *Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := openStore(filepath.Join(dir, "box.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.db.Close() })
	n, err := newNotifier(st, dir)
	if err != nil {
		t.Fatal(err)
	}
	return n, st
}

// Channel secrets are encrypted in the database and never handed back to the browser.
func TestNotifySecretsStayPrivate(t *testing.T) {
	n, st := newTestNotifier(t)
	id, err := n.saveChannel(notifyChannel{Kind: "email", Name: "Mail", Enabled: true, Config: map[string]string{
		"host": "smtp.example.com", "port": "587", "security": "starttls", "username": "box", "password": "hunter2-very-secret",
		"from": "box@example.com", "to": "me@example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	var raw string
	st.db.QueryRow(`SELECT config FROM notify_channels WHERE id = ?`, id).Scan(&raw)
	if strings.Contains(raw, "hunter2") {
		t.Fatal("SMTP password stored in clear text")
	}
	list := n.channelsPublic()
	b, _ := json.Marshal(list)
	if strings.Contains(string(b), "hunter2") {
		t.Fatal("secret returned to the browser")
	}
	// Saving with an empty secret keeps the stored one.
	if _, err := n.saveChannel(notifyChannel{ID: id, Kind: "email", Name: "Mail 2", Enabled: true, Config: map[string]string{
		"host": "smtp.example.com", "port": "587", "security": "starttls", "username": "box", "password": "",
		"from": "box@example.com", "to": "me@example.com"}}); err != nil {
		t.Fatal(err)
	}
	ch, _ := n.channel(id)
	if ch.Config["password"] != "hunter2-very-secret" {
		t.Fatalf("stored secret lost: %q", ch.Config["password"])
	}
}

func TestNotifyURLRules(t *testing.T) {
	for _, ok := range []string{"https://ntfy.sh", "https://hooks.example.com/x", "http://127.0.0.1:8099/hook", "http://10.0.0.5/hook", "http://[::1]:80/"} {
		if err := checkNotifyURL(ok); err != nil {
			t.Errorf("%s refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"ftp://x", "http://example.com/hook", "https://user:pw@example.com", "javascript:alert(1)", ""} {
		if err := checkNotifyURL(bad); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestNotifyDedupAndRules(t *testing.T) {
	var mu sync.Mutex
	got := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, string(b))
		mu.Unlock()
	}))
	defer srv.Close()
	n, _ := newTestNotifier(t)
	id, err := n.saveChannel(notifyChannel{Kind: "webhook", Name: "Hook", Enabled: true, Config: map[string]string{"url": srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	n.send(evAppDown, "app:kuma", "Uptime Kuma is down", "It stopped.")
	n.send(evAppDown, "app:kuma", "Uptime Kuma is down", "It stopped.") // same key: deduplicated
	n.send(evAppDown, "app:other", "Other is down", "It stopped.")
	n.send(evBackupOK, "b:1", "Backup done", "") // off by default
	n.wait()
	mu.Lock()
	if len(got) != 2 || !strings.Contains(strings.Join(got, " "), "Uptime Kuma is down") {
		t.Fatalf("sent %d: %v", len(got), got)
	}
	mu.Unlock()
	// A rule with no channel picked sends nowhere; picking the channel sends again.
	n.setRules(map[string]notifyRule{evAppDown: {Enabled: true, Channels: []int64{}}})
	n.send(evAppDown, "app:third", "Third is down", "")
	n.setRules(map[string]notifyRule{evAppDown: {Enabled: true, Channels: []int64{id}}})
	n.send(evAppDown, "app:fourth", "Fourth is down", "")
	n.wait()
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 3 || !strings.Contains(got[2], "Fourth") {
		t.Fatalf("rules not applied: %v", got)
	}
	if h := n.history(10); len(h) < 3 {
		t.Fatalf("history: %v", h)
	}
}

func TestNotifyRateLimit(t *testing.T) {
	n, _ := newTestNotifier(t)
	now := time.Now()
	ok := 0
	for i := 0; i < 100; i++ {
		if n.allowRate(now) {
			ok++
		}
	}
	if ok != notifyBurst {
		t.Fatalf("rate limit let %d through", ok)
	}
	if !n.allowRate(now.Add(notifyWindow + time.Second)) {
		t.Fatal("rate limit never resets")
	}
}
