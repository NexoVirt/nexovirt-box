package box

import (
	"net/http"
	"strconv"
	"testing"
	"time"
)

func TestBackupProblemsOnlyCurrent(t *testing.T) {
	now := time.Now().Unix()
	// Newest first, as backupHistory returns it.
	hist := []backupRecord{
		{ID: 9, At: now, Kind: "backup", App: "nextcloud", Target: "pbs", Status: "failed", Error: "pbs down"},
		{ID: 8, At: now - 10, Kind: "backup", App: "gitea", Target: "local", Status: "ok"},
		{ID: 7, At: now - 20, Kind: "backup", App: "gitea", Target: "pbs", Status: "failed"},       // gitea to local works, but pbs still fails: stays visible
		{ID: 6, At: now - 30, Kind: "backup", App: "whoami-copy", Target: "pbs", Status: "failed"}, // app deleted
		{ID: 5, At: now - 40, Kind: "backup", App: "box", Target: "gone", Status: "failed"},        // target deleted
		{ID: 4, At: now - 50, Kind: "backup", App: "nextcloud", Target: "local", Status: "ok"},
		{ID: 3, At: now - 60, Kind: "backup", App: "nextcloud", Target: "pbs", Status: "failed"}, // not the latest run
		{ID: 2, At: now - 70, Kind: "verify", App: "box", Target: "local", Status: "failed"},     // not a backup
		{ID: 1, At: now - 80, Kind: "backup", App: "box", Target: "local", Status: "failed", Dismissed: true},
	}
	apps := map[string]bool{"nextcloud": true, "gitea": true, "box": true}
	targets := map[string]bool{"pbs": true, "local": true}
	got := backupProblems(hist, apps, targets)
	if len(got) != 2 || got[0].ID != 9 || got[1].ID != 7 {
		t.Fatalf("want #9 and #7, got %+v", got)
	}
	// Unknown app list (helper down): do not hide failures because of it.
	if got := backupProblems(hist, nil, targets); len(got) != 3 {
		t.Fatalf("nil apps: want #9, #7 and #6, got %+v", got)
	}
	// A later success to the same target clears its failure.
	fixed := append([]backupRecord{{ID: 10, At: now + 10, Kind: "backup", App: "gitea", Target: "pbs", Status: "ok"}}, hist...)
	if got := backupProblems(fixed, apps, targets); len(got) != 1 || got[0].ID != 9 {
		t.Fatalf("after gitea/pbs success: want only #9, got %+v", got)
	}
}

func TestBackupHistoryDismissAndClear(t *testing.T) {
	app, h, token := newTestApp(t)
	sess := sessionFrom(post(h, "/api/setup", `{"token":"`+token+`","username":"admin","password":"long-enough-pw"}`, ""))
	now := time.Now().Unix()
	app.store.addBackupRecord(backupRecord{At: now - 40*86400, Kind: "backup", App: "a", Target: "t", Status: "failed"})
	app.store.addBackupRecord(backupRecord{At: now, Kind: "backup", App: "a", Target: "t", Status: "failed"})
	hist := app.store.backupHistory("", 10)
	if rec := post(h, "/api/backups/history/"+strconv.FormatInt(hist[0].ID, 10)+"/dismiss", ``, sess); rec.Code != http.StatusOK {
		t.Fatalf("dismiss: %d %s", rec.Code, rec.Body)
	}
	if !app.store.backupHistory("", 10)[0].Dismissed {
		t.Fatal("dismiss not stored")
	}
	// Clearing needs a fresh password.
	app.proofs.Store(hashToken(sess), strconv.FormatInt(time.Now().Add(-time.Hour).Unix(), 10)+".00")
	if rec := post(h, "/api/backups/history/clear", `{"older_than_days":30}`, sess); rec.Code != http.StatusPreconditionRequired {
		t.Fatalf("clear without proof: %d", rec.Code)
	}
	if rec := post(h, "/api/auth/reauth", `{"password":"long-enough-pw"}`, sess); rec.Code != http.StatusOK {
		t.Fatalf("reauth: %d", rec.Code)
	}
	if rec := post(h, "/api/backups/history/clear", `{"older_than_days":-1}`, sess); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("negative age: %d", rec.Code)
	}
	if rec := post(h, "/api/backups/history/clear", `{"older_than_days":30}`, sess); rec.Code != http.StatusOK {
		t.Fatalf("clear: %d %s", rec.Code, rec.Body)
	}
	if n := len(app.store.backupHistory("", 10)); n != 1 {
		t.Fatalf("older than 30 days: %d left, want 1", n)
	}
	if rec := post(h, "/api/backups/history/clear", `{"older_than_days":0}`, sess); rec.Code != http.StatusOK {
		t.Fatalf("clear all: %d", rec.Code)
	}
	if n := len(app.store.backupHistory("", 10)); n != 0 {
		t.Fatalf("clear all left %d", n)
	}
}
