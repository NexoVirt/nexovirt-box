package box

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Every route not on the explicit public list must refuse an unauthenticated caller.
func TestEveryPrivateRouteNeedsAuth(t *testing.T) {
	app, h, _ := newTestApp(t)
	public := map[string]bool{
		"GET /api/auth/state":            true,
		"POST /api/setup":                true,
		"POST /api/login":                true,
		"POST /api/logout":               true,
		"POST /api/login/2fa":            true,
		"POST /api/login/passkey/begin":  true,
		"POST /api/login/passkey/finish": true,
		"GET /s/{token}":                 true, // public share links
		"POST /s/{token}":                true,
		"GET /api/proxy-auth/check":      true, // Caddy's forward_auth, checks its own domain cookie
		"GET /api/proxy-auth/start":      true, // checks the session itself, redirects to sign-in
		"GET /api/proxy-auth/finish":     true, // one-time token on the app's domain
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

func TestOnlyFailedSignInsCount(t *testing.T) {
	_, h, token := newTestApp(t)
	setupAndLogin(t, h, token)
	for i := 0; i < 8; i++ {
		if rec := post(h, "/api/login", `{"username":"admin","password":"long-enough-pw"}`, ""); rec.Code != http.StatusOK {
			t.Fatalf("sign-in %d: got %d", i, rec.Code)
		}
	}
	for i := 0; i < 5; i++ {
		post(h, "/api/login", `{"username":"admin","password":"wrong-password"}`, "")
	}
	if rec := post(h, "/api/login", `{"username":"admin","password":"long-enough-pw"}`, ""); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("after 5 failures: got %d", rec.Code)
	}
}

// A burst of parallel wrong sign-ins must not get more than the limit to the password check.
func TestParallelWrongSignInsStayLimited(t *testing.T) {
	_, h, token := newTestApp(t)
	setupAndLogin(t, h, token)
	var wg sync.WaitGroup
	codes := make(chan int, 30)
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- post(h, "/api/login", `{"username":"admin","password":"wrong-password"}`, "").Code
		}()
	}
	wg.Wait()
	close(codes)
	checked := 0
	for c := range codes {
		if c == http.StatusUnauthorized {
			checked++
		}
	}
	if checked > 5 {
		t.Fatalf("%d parallel tries reached the password check, limit is 5", checked)
	}
}

func TestTOTPSignIn(t *testing.T) {
	app, h, token := newTestApp(t)
	sess := setupAndLogin(t, h, token)
	if rec := post(h, "/api/2fa/totp/begin", `{"password":"wrong-password"}`, sess); rec.Code == http.StatusOK {
		t.Fatal("2FA setup without the right password")
	}
	var begin map[string]string
	json.Unmarshal(post(h, "/api/2fa/totp/begin", `{"password":"long-enough-pw"}`, sess).Body.Bytes(), &begin)
	if !strings.HasPrefix(begin["qr"], "data:image/svg+xml;base64,") || begin["secret"] == "" {
		t.Fatalf("begin: %v", begin)
	}
	code := totpCode(begin["secret"], uint64(time.Now().Unix()/30))
	var enable struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	json.Unmarshal(post(h, "/api/2fa/totp/enable", `{"code":"`+code+`"}`, sess).Body.Bytes(), &enable)
	if len(enable.RecoveryCodes) != 10 {
		t.Fatalf("recovery codes: %v", enable.RecoveryCodes)
	}
	// Password alone no longer signs in.
	rec := post(h, "/api/login", `{"username":"admin","password":"long-enough-pw"}`, "")
	var lr map[string]any
	json.Unmarshal(rec.Body.Bytes(), &lr)
	if lr["two_factor"] != true || sessionFrom(rec) != "" {
		t.Fatalf("password alone: %v", lr)
	}
	pending := lr["pending"].(string)
	if rec := post(h, "/api/login/2fa", `{"pending":"`+pending+`","code":"000000"}`, ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong code: %d", rec.Code)
	}
	rc := enable.RecoveryCodes[0]
	if rec := post(h, "/api/login/2fa", `{"pending":"`+pending+`","code":"`+rc+`"}`, ""); rec.Code != http.StatusOK || sessionFrom(rec) == "" {
		t.Fatalf("recovery code: %d %s", rec.Code, rec.Body)
	}
	// The recovery code is single use.
	json.Unmarshal(post(h, "/api/login", `{"username":"admin","password":"long-enough-pw"}`, "").Body.Bytes(), &lr)
	if rec := post(h, "/api/login/2fa", `{"pending":"`+lr["pending"].(string)+`","code":"`+rc+`"}`, ""); rec.Code == http.StatusOK {
		t.Fatal("recovery code worked twice")
	}
	if app.store.recoveryLeft(1) != 9 {
		t.Fatalf("left: %d", app.store.recoveryLeft(1))
	}
	// The helper now also wants the code for the console.
	_, err := app.helper.call(context.Background(), helperRequest{Action: "console_proof", Session: hashToken(sess), Password: "long-enough-pw"}, nil)
	if err == nil {
		t.Fatal("console proof without the 2FA code")
	}
	code = totpCode(begin["secret"], uint64(time.Now().Unix()/30))
	if _, err := app.helper.call(context.Background(), helperRequest{Action: "console_proof", Session: hashToken(sess), Password: "long-enough-pw", Code: code}, nil); err != nil {
		t.Fatalf("console proof with code: %v", err)
	}
}

func TestPasskeysRefusedOnIPHost(t *testing.T) {
	app := &App{secure: true}
	r := httptest.NewRequest("POST", "https://10.0.0.5:8443/", nil)
	if _, err := app.webAuthnFor(r); err == nil {
		t.Fatal("passkeys allowed on an IP address")
	}
	r = httptest.NewRequest("POST", "https://box.example.com:8443/", nil)
	if _, err := app.webAuthnFor(r); err != nil {
		t.Fatalf("domain refused: %v", err)
	}
}

func TestPasswordChangeEndsOtherSessions(t *testing.T) {
	app, h, token := newTestApp(t)
	sess := setupAndLogin(t, h, token)
	other := sessionFrom(post(h, "/api/login", `{"username":"admin","password":"long-enough-pw"}`, ""))
	closed := make(chan struct{})
	app.consoles.add(hashToken(other), func() { close(closed) })

	var list struct{ Sessions []SessionInfo }
	json.Unmarshal(get(h, "/api/sessions", sess).Body.Bytes(), &list)
	current := 0
	for _, si := range list.Sessions {
		if si.Current {
			current++
		}
	}
	if len(list.Sessions) < 2 || current != 1 || list.Sessions[0].IP != "10.0.0.1" {
		t.Fatalf("sessions: %+v", list.Sessions)
	}
	if rec := post(h, "/api/account/password", `{"current":"wrong-password","new":"another-long-pw"}`, sess); rec.Code == 200 {
		t.Fatal("changed with a wrong current password")
	}
	if rec := post(h, "/api/account/password", `{"current":"long-enough-pw","new":"short"}`, sess); rec.Code == 200 {
		t.Fatal("accepted a short password")
	}
	if rec := post(h, "/api/account/password", `{"current":"long-enough-pw","new":"another-long-pw"}`, sess); rec.Code != 200 {
		t.Fatalf("change: %d %s", rec.Code, rec.Body)
	}
	select {
	case <-closed:
	default:
		t.Fatal("the other session's console stayed open")
	}
	if rec := get(h, "/api/system", other); rec.Code != http.StatusUnauthorized {
		t.Fatalf("other session still works: %d", rec.Code)
	}
	if rec := get(h, "/api/sessions", sess); rec.Code != 200 {
		t.Fatal("the changing session was signed out")
	}
	if rec := post(h, "/api/login", `{"username":"admin","password":"another-long-pw"}`, ""); rec.Code != 200 {
		t.Fatalf("new password does not work: %d", rec.Code)
	}
}

func TestPasswordChangeVoidsPendingTwoStepSignIn(t *testing.T) {
	app, h, token := newTestApp(t)
	sess := setupAndLogin(t, h, token)
	var begin map[string]string
	json.Unmarshal(post(h, "/api/2fa/totp/begin", `{"password":"long-enough-pw"}`, sess).Body.Bytes(), &begin)
	var enable struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	json.Unmarshal(post(h, "/api/2fa/totp/enable", `{"code":"`+totpCode(begin["secret"], uint64(time.Now().Unix()/30))+`"}`, sess).Body.Bytes(), &enable)
	startLogin := func() string {
		var lr map[string]any
		json.Unmarshal(post(h, "/api/login", `{"username":"admin","password":"long-enough-pw"}`, "").Body.Bytes(), &lr)
		p, _ := lr["pending"].(string)
		if p == "" {
			t.Fatalf("no pending sign-in: %v", lr)
		}
		return p
	}

	// Someone who knows the old password waits at the second step; the owner changes the password.
	pending := startLogin()
	if rec := post(h, "/api/account/password", `{"current":"long-enough-pw","new":"another-long-pw"}`, sess); rec.Code != 200 {
		t.Fatalf("change: %d %s", rec.Code, rec.Body)
	}
	if rec := post(h, "/api/login/2fa", `{"pending":"`+pending+`","code":"`+enable.RecoveryCodes[0]+`"}`, ""); rec.Code == http.StatusOK || sessionFrom(rec) != "" {
		t.Fatalf("a pending sign-in from before the change finished: %d", rec.Code)
	}

	// Even a pending entry that survived (password changed another way) is refused by the hash check.
	var lr map[string]any
	json.Unmarshal(post(h, "/api/login", `{"username":"admin","password":"another-long-pw"}`, "").Body.Bytes(), &lr)
	pending2 := lr["pending"].(string)
	app.store.setPassword(1, "third-long-password")
	if rec := post(h, "/api/login/2fa", `{"pending":"`+pending2+`","code":"`+enable.RecoveryCodes[1]+`"}`, ""); rec.Code == http.StatusOK {
		t.Fatal("a pending sign-in finished after the password hash changed")
	}
}

func postFrom(h http.Handler, ip, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "http://box.test"+path, strings.NewReader(body))
	req.Header.Set("Origin", "http://box.test")
	req.RemoteAddr = ip + ":1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// totpBox signs in, turns TOTP on and returns a function that starts a pending sign-in from ip.
func totpBox(t *testing.T) (h http.Handler, secret string, recovery []string, pending func(ip string) string) {
	t.Helper()
	_, h, token := newTestApp(t)
	sess := setupAndLogin(t, h, token)
	var begin map[string]string
	json.Unmarshal(post(h, "/api/2fa/totp/begin", `{"password":"long-enough-pw"}`, sess).Body.Bytes(), &begin)
	var en struct {
		RecoveryCodes []string `json:"recovery_codes"`
	}
	json.Unmarshal(post(h, "/api/2fa/totp/enable", `{"code":"`+totpCode(begin["secret"], uint64(time.Now().Unix()/30))+`"}`, sess).Body.Bytes(), &en)
	return h, begin["secret"], en.RecoveryCodes, func(ip string) string {
		var lr map[string]any
		json.Unmarshal(postFrom(h, ip, "/api/login", `{"username":"admin","password":"long-enough-pw"}`).Body.Bytes(), &lr)
		p, _ := lr["pending"].(string)
		return p
	}
}

func code2FA(h http.Handler, ip, pending, code string) int {
	return postFrom(h, ip, "/api/login/2fa", `{"pending":"`+pending+`","code":"`+code+`"}`).Code
}

// Guessing is capped per pending sign-in, per IP and per account, while the owner (another IP)
// still signs in and a recovery code is never held back by the TOTP limit.
func TestTwoFactorGuessLimits(t *testing.T) {
	h, secret, recovery, pending := totpBox(t)

	// Per pending sign-in: 5 tries, spread over IPs so the IP cap does not interfere.
	p := pending("10.1.0.1")
	for i := 0; i < 5; i++ {
		code2FA(h, fmt.Sprintf("10.2.0.%d", i+1), p, "000000")
	}
	if c := code2FA(h, "10.2.0.99", p, totpCode(secret, uint64(time.Now().Unix()/30))); c != http.StatusUnauthorized {
		t.Fatalf("6th try on one pending sign-in: %d", c)
	}

	// Per IP: fresh pending sign-ins give no new guesses.
	last := 0
	for i := 0; i < 6; i++ {
		last = code2FA(h, "10.3.0.1", pending("10.3.0.1"), "000000")
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("6th wrong code from one IP: %d", last)
	}

	// The owner on another IP signs in while that IP is blocked.
	if c := code2FA(h, "10.4.0.1", pending("10.4.0.1"), totpCode(secret, uint64(time.Now().Unix()/30))); c != http.StatusOK {
		t.Fatalf("owner blocked by someone else's guesses: %d", c)
	}

	// A recovery code works from the IP whose TOTP guesses are used up.
	if c := code2FA(h, "10.3.0.1", pending("10.3.0.1"), recovery[0]); c != http.StatusOK {
		t.Fatalf("recovery code held back by the TOTP limit: %d", c)
	}
}

// Many IPs together still hit the per-account backstop.
func TestTwoFactorAccountBackstop(t *testing.T) {
	old := tfaAccountMax
	tfaAccountMax = 8
	t.Cleanup(func() { tfaAccountMax = old })
	h, _, _, pending := totpBox(t)
	limited := false
	for i := 0; i < tfaAccountMax+10 && !limited; i++ {
		ip := fmt.Sprintf("10.5.%d.%d", i/250, i%250+1)
		limited = code2FA(h, ip, pending(ip), "000000") == http.StatusTooManyRequests
	}
	if !limited {
		t.Fatal("guesses from many IPs were never limited")
	}
}
