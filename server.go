package main

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
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
	store     *Store
	limiter   *limiter
	secure    bool
	sampler   *sampler
	tokenPath string
	trusted   []*net.IPNet // reverse proxies allowed to set X-Forwarded-For
	helper    *helperClient
	consoles  *consoleRegistry
	catalog   *catalogStore
	jobs      *jobs
	proofs    sync.Map // session hash -> console proof issued by the helper
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
		{"GET /api/jobs/{id}", false, a.jobStatus},
		{"GET /api/settings", false, a.settingsGet},
		{"POST /api/settings", false, a.settingsSave},
	}
}

func (a *App) routes(ui fs.FS) http.Handler {
	if a.consoles == nil {
		a.consoles = newConsoleRegistry()
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

func (a *App) startSession(w http.ResponseWriter, userID int64) error {
	t, err := a.store.newSession(userID)
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
	if err := a.startSession(w, 1); err != nil {
		log.Printf("setup session: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) login(w http.ResponseWriter, r *http.Request) {
	if !a.limiter.allow("login:" + a.clientIP(r)) {
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
	a.store.audit(c.Username, a.clientIP(r), "login.ok", "")
	t, err := a.store.newSession(id)
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
	writeJSON(w, http.StatusOK, map[string]bool{"fresh": a.consoleProof(sessionToken(r)) != ""})
}

func (a *App) activity(w http.ResponseWriter, r *http.Request) {
	items, err := a.store.recentAudit(200)
	if err != nil {
		log.Printf("activity: %v", err)
		writeError(w, http.StatusInternalServerError, "The activity log could not be read.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
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

// limiter counts attempts per key in SQLite, so a restart does not reset it.
type limiter struct {
	store  *Store
	max    int
	window time.Duration
}

func newLimiter(store *Store, max int, window time.Duration) *limiter {
	return &limiter{store: store, max: max, window: window}
}

// allow counts every attempt; it fails closed when the database cannot be read.
func (l *limiter) allow(key string) bool {
	ok, err := l.store.attempt(key, l.max, l.window)
	if err != nil {
		log.Printf("rate limit: %v", err)
		return false
	}
	return ok
}
