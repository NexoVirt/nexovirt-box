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
}

func (a *App) routes(ui fs.FS) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/auth/state", a.authState)
	mux.HandleFunc("POST /api/setup", a.setup)
	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("POST /api/logout", a.logout)
	mux.HandleFunc("GET /api/system", a.requireAuth(a.system))
	mux.HandleFunc("GET /api/stats/stream", a.requireAuth(a.statsStream))
	mux.HandleFunc("GET /api/console", a.requireAuth(a.console))
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

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---- auth ----

func (a *App) currentUser(r *http.Request) (int64, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return 0, false
	}
	return a.store.sessionUser(c.Value)
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
	if !a.limiter.allow("setup:" + clientIP(r)) {
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
	if !a.limiter.allow("login:" + clientIP(r)) {
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
		writeError(w, http.StatusUnauthorized, "The username or password is wrong.")
		return
	}
	if err := a.startSession(w, id); err != nil {
		log.Printf("login session: %v", err)
		writeError(w, http.StatusInternalServerError, "You could not be signed in. Check the service log.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.store.deleteSession(c.Value)
	}
	a.setCookie(w, "", -1)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
		log.Printf("Setup code (also in %s): %s", a.tokenPath, strings.TrimSpace(string(b)))
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

type limiter struct {
	mu     sync.Mutex
	max    int
	window time.Duration
	hits   map[string][]time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, hits: map[string][]time.Time{}}
}

// allow counts every attempt (ponytail: in memory, resets on restart; fine for one box).
func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if now.Sub(t) < l.window {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.max {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	return true
}
