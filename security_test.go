package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Every route not on the explicit public list must refuse an unauthenticated caller.
func TestEveryPrivateRouteNeedsAuth(t *testing.T) {
	app, h, _ := newTestApp(t)
	public := map[string]bool{
		"GET /api/auth/state": true,
		"POST /api/setup":     true,
		"POST /api/login":     true,
		"POST /api/logout":    true,
	}
	placeholder := regexp.MustCompile(`\{[^}]+\}`)
	n := 0
	for _, rt := range app.routeTable() {
		if rt.public != public[rt.pattern] {
			t.Fatalf("%s: public=%v, the public list says %v", rt.pattern, rt.public, public[rt.pattern])
		}
		if rt.public {
			continue
		}
		method, path, _ := strings.Cut(rt.pattern, " ")
		path = placeholder.ReplaceAllString(path, "x")
		req := httptest.NewRequest(method, "http://box.test"+path, strings.NewReader("{}"))
		req.Header.Set("Origin", "http://box.test")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("%s without a session: got %d, want 401", rt.pattern, rec.Code)
		}
		n++
	}
	if n < 5 {
		t.Fatalf("only %d private routes checked", n)
	}
}

func TestForwardedHeadersIgnoredWithoutTrustedProxy(t *testing.T) {
	app := &App{}
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "203.0.113.9:5000"
	r.Header.Set("X-Forwarded-For", "127.0.0.1")
	r.Header.Set("X-Real-IP", "127.0.0.1")
	if ip := app.clientIP(r); ip != "203.0.113.9" {
		t.Fatalf("untrusted proxy headers were used: %s", ip)
	}
	app.trusted = parseCIDRs("10.0.0.0/8")
	r.RemoteAddr = "10.1.2.3:443"
	r.Header.Set("X-Forwarded-For", "198.51.100.7, 10.9.9.9")
	if ip := app.clientIP(r); ip != "198.51.100.7" {
		t.Fatalf("trusted proxy: got %s", ip)
	}
	r.RemoteAddr = "203.0.113.9:5000" // not a trusted proxy: header ignored again
	if ip := app.clientIP(r); ip != "203.0.113.9" {
		t.Fatalf("header from untrusted peer used: %s", ip)
	}
}

func TestRateLimitSurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	db := filepath.Join(dir, "box.db")
	s1, _ := openStore(db)
	l1 := newLimiter(s1, 3, time.Minute)
	for i := 0; i < 3; i++ {
		l1.allow("login:1.2.3.4")
	}
	s1.db.Close()
	s2, _ := openStore(db)
	defer s2.db.Close()
	if newLimiter(s2, 3, time.Minute).allow("login:1.2.3.4") {
		t.Fatal("limit was reset by a restart")
	}
}

func TestDatabaseFilesPrivate(t *testing.T) {
	dir := t.TempDir()
	s, err := openStore(filepath.Join(dir, "box.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.db.Close()
	for _, f := range []string{"box.db", "box.db-wal", "box.db-shm"} {
		st, err := os.Stat(filepath.Join(dir, f))
		if err != nil {
			continue
		}
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("%s has mode %v", f, st.Mode().Perm())
		}
	}
}

func setupAndLogin(t *testing.T, h http.Handler, token string) string {
	t.Helper()
	post(h, "/api/setup", `{"token":"`+token+`","username":"admin","password":"long-enough-pw"}`, "")
	rec := post(h, "/api/login", `{"username":"admin","password":"long-enough-pw"}`, "")
	return sessionFrom(rec)
}

func TestConsoleNeedsRecentPassword(t *testing.T) {
	app, h, token := newTestApp(t)
	sess := setupAndLogin(t, h, token)

	var ready map[string]bool
	json.Unmarshal(get(h, "/api/console/ready", sess).Body.Bytes(), &ready)
	if !ready["fresh"] {
		t.Fatal("a fresh sign-in should count as a recent password")
	}
	// Age the proof past the window.
	app.proofs.Store(hashToken(sess), strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)+".00")
	json.Unmarshal(get(h, "/api/console/ready", sess).Body.Bytes(), &ready)
	if ready["fresh"] {
		t.Fatal("an old proof must not open the console")
	}
	if rec := get(h, "/api/console", sess); rec.Code != http.StatusForbidden {
		t.Fatalf("console with old proof: got %d", rec.Code)
	}
	if rec := post(h, "/api/auth/reauth", `{"password":"wrong-password"}`, sess); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: got %d", rec.Code)
	}
	if rec := post(h, "/api/auth/reauth", `{"password":"long-enough-pw"}`, sess); rec.Code != http.StatusOK {
		t.Fatalf("reauth: got %d %s", rec.Code, rec.Body)
	}
	json.Unmarshal(get(h, "/api/console/ready", sess).Body.Bytes(), &ready)
	if !ready["fresh"] {
		t.Fatal("reauth did not refresh the proof")
	}
	var acts struct{ Items []map[string]any }
	json.Unmarshal(get(h, "/api/activity", sess).Body.Bytes(), &acts)
	found := false
	for _, it := range acts.Items {
		if it["action"] == "reauth.failed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("failed reauth not in the activity log: %v", acts.Items)
	}
}

func TestLogoutClosesConsoles(t *testing.T) {
	app, h, token := newTestApp(t)
	sess := setupAndLogin(t, h, token)
	closed := make(chan struct{})
	app.consoles.add(hashToken(sess), func() { close(closed) })
	post(h, "/api/logout", "", sess)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("logout did not close the console")
	}
}

// startTestHelper runs the root helper on a temp socket, allowing the current user.
func startTestHelper(t *testing.T, webState string) string {
	t.Helper()
	sock := filepath.Join(t.TempDir(), "h.sock")
	if len(sock) > 100 {
		sock = filepath.Join(os.TempDir(), "nxh-"+randomToken()[:8]+".sock")
	}
	hs := &helperServer{allowUID: os.Getuid(), appsDir: t.TempDir(), webState: webState, proofs: newProofKeeper()}
	ln, err := hs.listen(sock, -1)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close(); os.Remove(sock) })
	go hs.serve(ln)
	return sock
}

func TestHelperRefusesUnknownAction(t *testing.T) {
	sock := startTestHelper(t, t.TempDir())
	c, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte(`{"action":"exec","cmd":"id"}` + "\n"))
	line, _ := bufio.NewReader(c).ReadString('\n')
	if !strings.Contains(line, `"ok":false`) {
		t.Fatalf("unknown action accepted: %s", line)
	}
}

func TestHelperRefusesOtherUsers(t *testing.T) {
	hs := &helperServer{allowUID: 4242}
	if hs.peerAllowed(4243) || !hs.peerAllowed(4242) || !hs.peerAllowed(0) {
		t.Fatal("peer check wrong")
	}
}

func TestHelperConsolePassesTerminal(t *testing.T) {
	app, h, token := newTestApp(t)
	sess := setupAndLogin(t, h, token)
	tty, conn, err := app.helper.console(hashToken(sess), app.consoleProof(sess), 80, 24)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	defer tty.Close()
	tty.Write([]byte("echo NXOK_$((6*7))\n"))
	buf := make([]byte, 4096)
	var out strings.Builder
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && !strings.Contains(out.String(), "NXOK_42") {
		tty.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		n, _ := tty.Read(buf)
		out.Write(buf[:n])
	}
	if !strings.Contains(out.String(), "NXOK_42") {
		t.Fatalf("no shell output: %q", out.String())
	}
}

// The helper itself refuses a console without a valid proof bound to a live session, so a
// compromised web process cannot open one on its own.
func TestHelperEnforcesConsoleProof(t *testing.T) {
	app, h, token := newTestApp(t)
	sess := setupAndLogin(t, h, token)
	proof := app.consoleProof(sess)
	if proof == "" {
		t.Fatal("sign-in did not get a console proof from the helper")
	}
	sh := hashToken(sess)
	exp, _, _ := strings.Cut(proof, ".")
	for name, tc := range map[string][2]string{
		"no proof":       {sh, ""},
		"forged mac":     {sh, exp + ".deadbeef"},
		"other session":  {hashToken("someone-else"), proof},
		"extended clock": {sh, strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10) + proof[len(exp):]},
	} {
		if tty, c, err := app.helper.console(tc[0], tc[1], 80, 24); err == nil {
			tty.Close()
			c.Close()
			t.Fatalf("%s: console opened", name)
		}
	}
	// A valid proof stops working once the session is gone.
	post(h, "/api/logout", "", sess)
	if tty, c, err := app.helper.console(sh, proof, 80, 24); err == nil {
		tty.Close()
		c.Close()
		t.Fatal("console opened for a signed-out session")
	}
}

func TestHelperProofNeedsRightPassword(t *testing.T) {
	app, h, token := newTestApp(t)
	sess := setupAndLogin(t, h, token)
	_, err := app.helper.call(context.Background(), helperRequest{Action: "console_proof", Session: hashToken(sess), Password: "wrong-password"}, nil)
	if err == nil || !strings.Contains(err.Error(), "wrong") {
		t.Fatalf("wrong password accepted: %v", err)
	}
}

func TestTOTP(t *testing.T) {
	// RFC 6238 test vector (SHA-1, secret "12345678901234567890").
	secret := b32.EncodeToString([]byte("12345678901234567890"))
	if got := totpCode(secret, 59/30); got != "287082" {
		t.Fatalf("totp vector: %s", got)
	}
	now := time.Unix(59, 0)
	if !totpValid(secret, "287082", now) || totpValid(secret, "287083", now) || totpValid(secret, "", now) {
		t.Fatal("totp check wrong")
	}
}
