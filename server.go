package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const sessionCookie = "nxbox_session"

type App struct {
	store      *Store
	limiter    *limiter
	secure     bool
	sampler    *sampler
	tokenPath  string
	trusted    []*net.IPNet // reverse proxies allowed to set X-Forwarded-For
	helper     *helperClient
	consoles   *consoleRegistry
	catalog    *catalogStore
	jobs       *jobs
	proofs     sync.Map // session hash -> console proof issued by the helper
	tfa        *twoFactor
	port       int // listen port (the helper checks it after a self-update)
	updates    updateCache
	disks      diskCache
	shareLimit *limiter // public share link hits per IP
}

type route struct {
	pattern string
	public  bool
	h       http.HandlerFunc
}

// routeTable lists every API route. Everything is private unless marked public, and the
// test walks this table unauthenticated (deny by default).
func (a *App) routeTable() []route {
	return []route{
		{"GET /api/auth/state", true, a.authState},
		{"POST /api/setup", true, a.setup},
		{"POST /api/login", true, a.login},
		{"POST /api/logout", true, a.logout},
		{"POST /api/login/2fa", true, a.login2FA},
		{"POST /api/login/passkey/begin", true, a.loginPasskeyBegin},
		{"POST /api/login/passkey/finish", true, a.loginPasskeyFinish},
		{"GET /api/2fa", false, a.twoFactorStatus},
		{"POST /api/2fa/totp/begin", false, a.totpBegin},
		{"POST /api/2fa/totp/enable", false, a.totpEnable},
		{"POST /api/2fa/totp/disable", false, a.totpDisable},
		{"POST /api/2fa/recovery", false, a.recoveryRegenerate},
		{"POST /api/2fa/passkey/begin", false, a.passkeyBegin},
		{"POST /api/2fa/passkey/finish", false, a.passkeyFinish},
		{"POST /api/2fa/passkey/{id}/delete", false, a.passkeyDelete},
		{"POST /api/auth/reauth", false, a.reauth},
		{"GET /api/system", false, a.system},
		{"GET /api/stats/stream", false, a.statsStream},
		{"GET /api/console/ready", false, a.consoleReady},
		{"GET /api/console", false, a.console},
		{"GET /api/activity", false, a.activity},
		{"GET /api/docker", false, a.dockerInfo},
		{"POST /api/docker/install", false, a.dockerInstall},
		{"GET /api/catalog", false, a.catalogList},
		{"POST /api/catalog/sync", false, a.catalogSync},
		{"GET /api/catalog/{id}", false, a.catalogApp},
		{"GET /api/catalog/{id}/icon", false, a.catalogIcon},
		{"GET /api/catalog/{id}/screenshot/{n}", false, a.catalogScreenshot},
		{"GET /api/apps", false, a.appsList},
		{"POST /api/apps/{id}/install", false, a.appInstall},
		{"POST /api/apps/{id}/action", false, a.appAction},
		{"POST /api/apps/{id}/uninstall", false, a.appUninstall},
		{"GET /api/apps/{id}/logs", false, a.appLogs},
		{"GET /api/jobs", false, a.jobsRunning},
		{"GET /api/jobs/{id}", false, a.jobStatus},
		{"GET /api/apps/{id}/credentials", false, a.appCredentials},
		{"GET /api/settings", false, a.settingsGet},
		{"GET /api/updates", false, a.updatesGet},
		{"POST /api/updates/check", false, a.updatesCheck},
		{"POST /api/updates/box/apply", false, a.boxUpdateApply},
		{"GET /api/images/unused", false, a.imagesUnused},
		{"POST /api/images/prune", false, a.imagesPrune},
		{"GET /api/proxy", false, a.proxyStatus},
		{"POST /api/proxy", false, a.proxySetup},
		{"POST /api/proxy/remove", false, a.proxyRemove},
		{"POST /api/apps/{id}/domain", false, a.appDomain},
		{"GET /api/firewall", false, a.firewallStatus},
		{"POST /api/firewall", false, a.firewallApply},
		{"POST /api/firewall/confirm", false, a.firewallConfirm},
		{"GET /api/files", false, a.filesList},
		{"GET /api/files/download", false, a.filesDownload},
		{"POST /api/files/upload", false, a.filesUpload},
		{"POST /api/files/mkdir", false, a.filesMutate("files_mkdir", "files.mkdir")},
		{"POST /api/files/rename", false, a.filesMutate("files_rename", "files.move")},
		{"POST /api/files/delete", false, a.filesMutate("files_delete", "files.delete")},
		{"POST /api/files/roots", false, a.filesRootsSet},
		{"POST /api/files/copy", false, a.filesMutate("files_copy", "files.copy")},
		{"GET /api/files/size", false, a.filesSize},
		{"GET /api/files/zip", false, a.filesZip},
		{"POST /api/settings", false, a.settingsSave},
		{"GET /api/sessions", false, a.sessionsList},
		{"POST /api/sessions/end-others", false, a.sessionsEndOthers},
		{"POST /api/account/password", false, a.passwordChange},
		{"GET /api/system-updates", false, a.sysUpdates},
		{"POST /api/system-updates/refresh", false, a.sysUpdatesRefresh},
		{"POST /api/system-updates/simulate", false, a.sysUpdatesSimulate},
		{"POST /api/system-updates/start", false, a.sysUpdatesStart},
		{"GET /api/system-updates/progress", false, a.sysUpdatesProgress},
		{"POST /api/system-updates/auto", false, a.sysAutoUpdates},
		{"POST /api/system/reboot", false, a.sysReboot},
		{"POST /api/system/install", false, a.pkgInstall},
		{"GET /api/disks", false, a.disksList},
		{"GET /api/disks/summary", false, a.disksSummary},
		{"POST /api/disks/selftest", false, a.diskSelftest},
		{"GET /api/shares", false, a.sharesStatus},
		{"POST /api/shares/samba", false, a.sambaShareSave},
		{"POST /api/shares/samba/{id}/delete", false, a.sambaShareDelete},
		{"POST /api/shares/samba-users", false, a.sambaUserSave},
		{"POST /api/shares/samba/handover", false, a.sambaHandover},
		{"POST /api/shares/samba-users/{name}/delete", false, a.sambaUserDelete},
		{"POST /api/shares/nfs", false, a.nfsExportSave},
		{"POST /api/shares/nfs/{id}/delete", false, a.nfsExportDelete},
		{"GET /api/share-links", false, a.shareLinksList},
		{"POST /api/share-links", false, a.shareLinkCreate},
		{"POST /api/share-links/{id}/revoke", false, a.shareLinkRevoke},
		{"GET /s/{token}", true, a.sharePublic},
		{"POST /s/{token}", true, a.sharePublic},
	}
}

func (a *App) routes(ui fs.FS) http.Handler {
	if a.consoles == nil {
		a.consoles = newConsoleRegistry()
	}
	if a.tfa == nil {
		a.tfa = newTwoFactor()
	}
	if a.shareLimit == nil {
		a.shareLimit = newLimiter(a.store, 60, time.Minute)
	}
	mux := http.NewServeMux()
	for _, rt := range a.routeTable() {
		h := rt.h
		if !rt.public {
			h = a.requireAuth(h)
		}
		mux.HandleFunc(rt.pattern, h)
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "This page does not exist.")
	})
	mux.Handle("/", spa(ui))
	return securityHeaders(sameOrigin(mux))
}

// spa serves the built UI and falls back to index.html so client routes reload fine.
func spa(ui fs.FS) http.Handler {
	files := http.FileServerFS(ui)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(ui, p); err == nil {
				files.ServeHTTP(w, r)
				return
			}
		}
		r2 := r.Clone(r.Context())
		r2.URL.Path = "/"
		files.ServeHTTP(w, r2)
	})
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// xterm.js writes its theme into a <style> element, so styles allow inline; scripts never do.
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// sameOrigin refuses state-changing requests from other sites (CSRF). The session cookie is
// SameSite=Strict as well; this is the second layer.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && !originMatches(r) {
			writeError(w, http.StatusForbidden, "This request was refused. Reload the page and try again.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func originMatches(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return false
	}
	u, err := url.Parse(o)
	return err == nil && u.Host == r.Host
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// clientIP is the TCP peer. X-Forwarded-For is only read when the peer is a configured
// trusted proxy, and then the right-most address that is not itself a trusted proxy wins.
// X-Real-IP and friends are never trusted.
func (a *App) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if !a.isTrusted(host) {
		return host
	}
	parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(parts) - 1; i >= 0; i-- {
		ip := strings.TrimSpace(parts[i])
		if net.ParseIP(ip) == nil {
			break
		}
		if !a.isTrusted(ip) {
			return ip
		}
	}
	return host
}

func (a *App) isTrusted(ip string) bool {
	p := net.ParseIP(ip)
	if p == nil {
		return false
	}
	for _, n := range a.trusted {
		if n.Contains(p) {
			return true
		}
	}
	return false
}

func parseCIDRs(list string) []*net.IPNet {
	var out []*net.IPNet
	for _, c := range strings.Split(list, ",") {
		if c = strings.TrimSpace(c); c == "" {
			continue
		}
		if !strings.Contains(c, "/") {
			if strings.Contains(c, ":") {
				c += "/128"
			} else {
				c += "/32"
			}
		}
		if _, n, err := net.ParseCIDR(c); err == nil {
			out = append(out, n)
		} else {
			log.Printf("trusted proxy %q ignored: %v", c, err)
		}
	}
	return out
}

// ---- auth ----

func (a *App) currentUser(r *http.Request) (int64, bool) {
	return a.store.sessionUser(sessionToken(r))
}

func sessionToken(r *http.Request) string {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return ""
	}
	return c.Value
}

// audit records who did what from where (never secrets).
func (a *App) audit(r *http.Request, action, detail string) {
	name := ""
	if id, ok := a.currentUser(r); ok {
		name = a.store.username(id)
	}
	a.store.audit(name, a.clientIP(r), action, detail)
}

func (a *App) requireAuth(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := a.currentUser(r); !ok {
			writeError(w, http.StatusUnauthorized, "Please sign in again.")
			return
		}
		h(w, r)
	}
}

func (a *App) setCookie(w http.ResponseWriter, token string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   a.secure,
		SameSite: http.SameSiteStrictMode,
	})
}

func (a *App) startSession(w http.ResponseWriter, r *http.Request, userID int64) error {
	t, err := a.store.newSession(userID, a.clientIP(r), r.UserAgent())
	if err != nil {
		return err
	}
	a.setCookie(w, t, int(sessionTTL.Seconds()))
	return nil
}

func (a *App) authState(w http.ResponseWriter, r *http.Request) {
	has, err := a.store.hasUser()
	if err != nil {
		log.Printf("auth state: %v", err)
		writeError(w, http.StatusInternalServerError, "NexoVirt Box could not read its database. Check the service log.")
		return
	}
	id, ok := a.currentUser(r)
	resp := map[string]any{"setup_needed": !has, "logged_in": ok, "version": version}
	if ok {
		resp["username"] = a.store.username(id)
	}
	writeJSON(w, http.StatusOK, resp)
}

type credentials struct {
	Token    string `json:"token"`
	Username string `json:"username"`
	Password string `json:"password"`
}

var usernameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

const minPassword = 10

func readCredentials(r *http.Request) (credentials, error) {
	var c credentials
	r.Body = http.MaxBytesReader(nil, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		return c, errors.New("The request could not be read.")
	}
	c.Username = strings.TrimSpace(strings.ToLower(c.Username))
	return c, nil
}

func (a *App) setup(w http.ResponseWriter, r *http.Request) {
	if !a.limiter.allow("setup:" + a.clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "Too many attempts. Wait a minute and try again.")
		return
	}
	c, err := readCredentials(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if has, _ := a.store.hasUser(); has {
		writeError(w, http.StatusConflict, "This Box is already set up. Sign in instead.")
		return
	}
	if !a.checkSetupToken(c.Token) {
		writeError(w, http.StatusForbidden, "The setup code is wrong. It is shown after the install and stored in "+a.tokenPath+".")
		return
	}
	if !usernameRe.MatchString(c.Username) {
		writeError(w, http.StatusUnprocessableEntity, "Use 1 to 32 lowercase letters, digits, dots, dashes or underscores for the username.")
		return
	}
	if len(c.Password) < minPassword || len(c.Password) > 72 {
		writeError(w, http.StatusUnprocessableEntity, "The password needs 10 to 72 characters.")
		return
	}
	if err := a.store.createUser(c.Username, c.Password); err != nil {
		if errors.Is(err, errUserExists) {
			writeError(w, http.StatusConflict, "This Box is already set up. Sign in instead.")
			return
		}
		log.Printf("setup: %v", err)
		writeError(w, http.StatusInternalServerError, "The account could not be saved. Check the service log.")
		return
	}
	os.Remove(a.tokenPath)
	if err := a.startSession(w, r, 1); err != nil {
		log.Printf("setup session: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	release, ok := a.limiter.reserve("login:" + a.clientIP(r))
	if !ok {
		writeError(w, http.StatusTooManyRequests, "Too many sign-in attempts. Wait a minute and try again.")
		return
	}
	c, err := readCredentials(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id, ok := a.store.checkLogin(c.Username, c.Password)
	if !ok {
		a.store.audit(c.Username, a.clientIP(r), "login.failed", "")
		writeError(w, http.StatusUnauthorized, "The username or password is wrong.")
		return
	}
	release() // only failed sign-ins count toward the limit
	if totp, keys := a.store.has2FA(id); totp || keys {
		tok, methods := a.startPending(id, c.Username)
		writeJSON(w, http.StatusOK, map[string]any{"two_factor": true, "pending": tok, "methods": methods})
		return
	}
	a.store.audit(c.Username, a.clientIP(r), "login.ok", "")
	t, err := a.store.newSession(id, a.clientIP(r), r.UserAgent())
	if err != nil {
		log.Printf("login session: %v", err)
		writeError(w, http.StatusInternalServerError, "You could not be signed in. Check the service log.")
		return
	}
	a.setCookie(w, t, int(sessionTTL.Seconds()))
	a.refreshProof(r, t, c.Password, "") // a fresh sign-in opens the console for a few minutes
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if t := sessionToken(r); t != "" {
		if _, ok := a.currentUser(r); ok {
			a.audit(r, "logout", "")
		}
		a.consoles.closeAll(hashToken(t)) // an open console ends with the session
		a.proofs.Delete(hashToken(t))
		a.store.deleteSession(t)
	}
	a.setCookie(w, "", -1)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) reauth(w http.ResponseWriter, r *http.Request) {
	if !a.limiter.allow("reauth:" + a.clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "Too many attempts. Wait a minute and try again.")
		return
	}
	var c struct {
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	r.Body = http.MaxBytesReader(nil, r.Body, 4096)
	if json.NewDecoder(r.Body).Decode(&c) != nil {
		writeError(w, http.StatusBadRequest, "The request could not be read.")
		return
	}
	if err := a.refreshProof(r, sessionToken(r), c.Password, c.Code); err != nil {
		a.audit(r, "reauth.failed", "")
		var ue userError
		if errors.As(err, &ue) {
			writeError(w, http.StatusUnauthorized, ue.msg)
			return
		}
		writeError(w, http.StatusBadGateway, "The password could not be checked. Check the helper service.")
		return
	}
	a.audit(r, "reauth.ok", "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// refreshProof asks the helper to check the password (and 2FA code) and keeps the proof it
// returns for this session. On failure the session has no proof.
func (a *App) refreshProof(r *http.Request, token, password, code string) error {
	sess := hashToken(token)
	a.proofs.Delete(sess)
	raw, err := a.helper.call(r.Context(), helperRequest{Action: "console_proof", Session: sess, Password: password, Code: code}, nil)
	if err != nil {
		return err
	}
	var out struct{ Proof string }
	if json.Unmarshal(raw, &out) != nil || out.Proof == "" {
		return errors.New("empty proof")
	}
	a.proofs.Store(sess, out.Proof)
	return nil
}

// consoleProof returns the session's proof while it is valid.
func (a *App) consoleProof(token string) string {
	v, ok := a.proofs.Load(hashToken(token))
	if !ok {
		return ""
	}
	p := v.(string)
	exp, _, _ := strings.Cut(p, ".")
	if n, err := strconv.ParseInt(exp, 10, 64); err != nil || time.Now().Unix() >= n {
		return ""
	}
	return p
}

func (a *App) consoleReady(w http.ResponseWriter, r *http.Request) {
	uid, _ := a.currentUser(r)
	writeJSON(w, http.StatusOK, map[string]bool{"fresh": a.consoleProof(sessionToken(r)) != "", "needs_code": a.store.totpSecret(uid) != ""})
}

// activity returns one page of the log: ?limit=50&cursor=<id>&order=asc|desc&type=&user=&q=.
func (a *App) activity(w http.ResponseWriter, r *http.Request) {
	v := r.URL.Query()
	limit, _ := strconv.Atoi(v.Get("limit"))
	if limit < 1 || limit > 200 {
		limit = 50
	}
	cursor, _ := strconv.ParseInt(v.Get("cursor"), 10, 64)
	q := auditQuery{Limit: limit, Cursor: cursor, Asc: v.Get("order") == "asc", Category: v.Get("type"),
		User: v.Get("user"), Search: strings.TrimSpace(v.Get("q"))}
	if len(q.Search) > 100 {
		q.Search = q.Search[:100]
	}
	items, total, err := a.store.queryAudit(q)
	if err != nil {
		log.Printf("activity: %v", err)
		writeError(w, http.StatusInternalServerError, "The activity log could not be read.")
		return
	}
	var next int64
	if len(items) == limit {
		next = items[len(items)-1].ID
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "total": total, "next": next, "users": a.store.auditUsers()})
}

// ---- setup token ----

// ensureSetupToken writes a one-time code while no account exists, so only someone with
// access to the server (install output or the file) can claim a fresh Box.
func (a *App) ensureSetupToken(state string) error {
	a.tokenPath = filepath.Join(state, "setup-token")
	has, err := a.store.hasUser()
	if err != nil {
		return err
	}
	if has {
		os.Remove(a.tokenPath)
		return nil
	}
	if b, err := os.ReadFile(a.tokenPath); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		log.Printf("Waiting for setup. The setup code is in %s.", a.tokenPath)
		return nil
	}
	t := randomToken()[:12]
	if err := os.WriteFile(a.tokenPath, []byte(t+"\n"), 0o600); err != nil {
		return err
	}
	log.Printf("Setup code (also in %s): %s", a.tokenPath, t)
	return nil
}

func (a *App) checkSetupToken(given string) bool {
	b, err := os.ReadFile(a.tokenPath)
	if err != nil {
		return false
	}
	want := strings.TrimSpace(string(b))
	given = strings.TrimSpace(given)
	return want != "" && subtle.ConstantTimeCompare([]byte(want), []byte(given)) == 1
}

// ---- rate limit ----

// limiter counts attempts per key in SQLite, so a restart does not reset it. One mutex makes
// "count, then record" atomic, so a burst of parallel requests cannot all slip past the count.
type limiter struct {
	mu     sync.Mutex
	store  *Store
	max    int
	window time.Duration
}

func newLimiter(store *Store, max int, window time.Duration) *limiter {
	return &limiter{store: store, max: max, window: window}
}

// reserve takes one try for key BEFORE the check it guards; it fails closed when the database
// cannot be read. Call release when the check passed, so successful tries do not count.
func (l *limiter) reserve(key string) (release func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	id, ok, err := l.store.attempt(key, l.max, l.window)
	if err != nil {
		log.Printf("rate limit: %v", err)
		return func() {}, false
	}
	return func() { l.store.releaseAttempt(id) }, ok
}

// allow counts every try (also successful ones).
func (l *limiter) allow(key string) bool {
	_, ok := l.reserve(key)
	return ok
}

// ---- account: sessions and password ----

func (a *App) sessionsList(w http.ResponseWriter, r *http.Request) {
	uid, _ := a.currentUser(r)
	writeJSON(w, http.StatusOK, map[string]any{"sessions": a.store.sessionsFor(uid, hashToken(sessionToken(r))),
		"trusted_proxies": a.trustedText()})
}

func (a *App) trustedText() string {
	var out []string
	for _, n := range a.trusted {
		out = append(out, n.String())
	}
	return strings.Join(out, ", ")
}

// endOthers signs out every other browser and closes their consoles.
func (a *App) endOthers(uid int64, keep string) int {
	gone := a.store.endOtherSessions(uid, keep)
	a.tfa.forgetUser(uid, gone)
	for _, h := range gone {
		a.consoles.closeAll(h)
		a.proofs.Delete(h)
	}
	return len(gone)
}

func (a *App) sessionsEndOthers(w http.ResponseWriter, r *http.Request) {
	var c struct {
		Password string `json:"password"`
	}
	if err := readJSON(r, &c); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	uid, err := a.checkPassword(r, c.Password)
	if err != nil {
		fail(w, err)
		return
	}
	n := a.endOthers(uid, hashToken(sessionToken(r)))
	a.audit(r, "account.sessions_end", fmt.Sprintf("%d other sessions", n))
	writeJSON(w, http.StatusOK, map[string]any{"ended": n})
}

func (a *App) passwordChange(w http.ResponseWriter, r *http.Request) {
	var c struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := readJSON(r, &c); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(c.New) < minPassword || len(c.New) > 72 {
		writeError(w, http.StatusUnprocessableEntity, fmt.Sprintf("The new password needs %d to 72 characters.", minPassword))
		return
	}
	uid, err := a.checkPassword(r, c.Current)
	if err != nil {
		fail(w, err)
		return
	}
	if err := a.store.setPassword(uid, c.New); err != nil {
		log.Printf("password change: %v", err)
		writeError(w, http.StatusInternalServerError, "The password could not be saved. Check the service log.")
		return
	}
	n := a.endOthers(uid, hashToken(sessionToken(r)))
	a.audit(r, "account.password", fmt.Sprintf("%d other sessions signed out", n))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "ended": n})
}
