package box

import (
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestRemoteDenied(t *testing.T) {
	for _, c := range []struct {
		path, method, scope string
		denied              bool
	}{
		{"/api/system", "GET", "full", false},
		{"/api/apps/x/action", "POST", "full", false},
		{"/api/apps/x/action", "POST", "read", true},
		{"/api/apps/x/credentials", "GET", "read", true},
		{"/api/apps/x/credentials", "GET", "full", false},
		{"/api/console", "GET", "full", true},
		{"/api/2fa/totp/disable", "POST", "full", true},
		{"/api/pairing", "POST", "full", true},
		{"/api/remote/1/system", "GET", "full", true},
		{"/api/account/password", "POST", "full", true},
		{"/api/../api/console", "GET", "full", true},
		{"/api//console", "GET", "full", true},
		{"/s/token", "GET", "full", true},
		{"/api/system", "GET", "other", true},
	} {
		if got := remoteDenied(c.path, c.method, c.scope); got != c.denied {
			t.Errorf("%s %s %s: denied=%v", c.method, c.path, c.scope, got)
		}
	}
}

// A pairing token works on allowed paths only, never as a session, and a box added with it is
// reached through the pinned server-side proxy.
func TestPairingAndProxy(t *testing.T) {
	remote, rh, rtoken := newTestApp(t)
	post(rh, "/api/setup", `{"token":"`+rtoken+`","username":"admin","password":"long-enough-pw"}`, "")
	remote.store.initRemote()
	tok := pairPrefix + randomToken()
	remote.store.db.Exec(`INSERT INTO pair_tokens (token_hash, user_id, name, scope, created) VALUES (?, 1, 'main', 'full', ?)`, hashToken(tok), time.Now().Unix())
	call := func(method, path, token string) int {
		r := httptest.NewRequest(method, "https://box.test"+path, strings.NewReader("{}"))
		r.Header.Set("Authorization", "Bearer "+token)
		rec := httptest.NewRecorder()
		rh.ServeHTTP(rec, r)
		return rec.Code
	}
	if c := call("GET", "/api/apps/auto-updates", tok); c != 200 {
		t.Fatalf("allowed path: %d", c)
	}
	if c := call("GET", "/api/2fa", tok); c != 403 {
		t.Fatalf("2fa with a pairing token: %d", c)
	}
	if c := call("POST", "/api/apps/x/auto-update", tok); c == 403 || c == 401 {
		t.Fatalf("POST without Origin refused for a paired box: %d", c)
	}
	if c := call("GET", "/api/apps/auto-updates", pairPrefix+"wrong"); c != 401 {
		t.Fatalf("bad token: %d", c)
	}

	srv := httptest.NewTLSServer(rh)
	defer srv.Close()
	sum := sha256.Sum256(srv.Certificate().Raw)
	fp := hex.EncodeToString(sum[:])

	local, lh, ltoken := newTestApp(t)
	local.state = t.TempDir()
	sess := sessionFrom(post(lh, "/api/setup", `{"token":"`+ltoken+`","username":"me","password":"long-enough-pw"}`, ""))
	local.store.initRemote()
	sealed, _ := local.sealToken(tok)
	local.store.db.Exec(`INSERT INTO remotes (name, url, token, fingerprint, created) VALUES ('nas', ?, ?, ?, 1)`, srv.URL, sealed, fp)
	if rec := get(lh, "/api/remote/1/apps/auto-updates", sess); rec.Code != 200 {
		t.Fatalf("proxy: %d %s", rec.Code, rec.Body)
	}
	if rec := get(lh, "/api/remote/1/2fa", sess); rec.Code != 403 {
		t.Fatalf("proxied denied path: %d", rec.Code)
	}
	if rec := get(lh, "/api/remote/1/apps/auto-updates", ""); rec.Code != 401 {
		t.Fatalf("proxy without a session: %d", rec.Code)
	}
	// A different certificate than the pinned one is refused.
	local.store.db.Exec(`UPDATE remotes SET fingerprint = ?`, strings.Repeat("0", 64))
	if rec := get(lh, "/api/remote/1/apps/auto-updates", sess); rec.Code != http.StatusBadGateway {
		t.Fatalf("pin not checked: %d", rec.Code)
	}
	// The token is stored sealed, never in clear.
	var stored string
	local.store.db.QueryRow(`SELECT token FROM remotes`).Scan(&stored)
	if strings.Contains(stored, tok) || local.openToken(stored) != tok {
		t.Fatal("token not sealed")
	}
}
