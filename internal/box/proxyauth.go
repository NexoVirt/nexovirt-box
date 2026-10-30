package box

// Box sign-in in front of an app domain (proxy.go writes the Caddy side). Caddy asks
// /api/proxy-auth/check for every request to a protected domain. Without a valid cookie the
// answer is a redirect to Box (/api/proxy-auth/start), which, for a signed-in user, sends a
// one-time token back to https://<domain>/.nxbox-auth; Caddy hands that to /api/proxy-auth/finish,
// which sets a cookie for that domain only. The cookie is NOT a Box session: it only opens that
// one domain, and it dies with the Box session it came from (sign-out ends it), so an app that
// reads its own cookies cannot use it against Box.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	proxyCookie     = "nxbox_proxy"
	proxyOnceTTL    = time.Minute
	proxyCookieTTL  = 12 * time.Hour
	proxyBoxURLSave = "proxy_box_url"
)

func (s *Store) initProxyTokens() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS proxy_tokens (
		token_hash TEXT PRIMARY KEY,
		domain TEXT NOT NULL,
		session_hash TEXT NOT NULL,
		kind TEXT NOT NULL,
		expires_at INTEGER NOT NULL
	)`)
}

func (s *Store) addProxyToken(domain, sessionHash, kind string, ttl time.Duration) string {
	s.initProxyTokens()
	t := randomToken()
	s.db.Exec(`DELETE FROM proxy_tokens WHERE expires_at < ?`, time.Now().Unix())
	s.db.Exec(`INSERT INTO proxy_tokens (token_hash, domain, session_hash, kind, expires_at) VALUES (?, ?, ?, ?, ?)`,
		hashToken(t), domain, sessionHash, kind, time.Now().Add(ttl).Unix())
	return t
}

// proxyTokenOK checks a token for a domain; the Box session it came from must still be alive.
// A one-time token is used up by the check.
func (s *Store) proxyTokenOK(token, domain, kind string) (string, bool) {
	if token == "" || domain == "" {
		return "", false
	}
	s.initProxyTokens()
	var sess string
	err := s.db.QueryRow(`SELECT p.session_hash FROM proxy_tokens p JOIN sessions s ON s.token_hash = p.session_hash
		WHERE p.token_hash = ? AND p.domain = ? AND p.kind = ? AND p.expires_at > ? AND s.expires_at > ?`,
		hashToken(token), domain, kind, time.Now().Unix(), time.Now().Unix()).Scan(&sess)
	if err != nil {
		return "", false
	}
	if kind == "once" {
		res, err := s.db.Exec(`DELETE FROM proxy_tokens WHERE token_hash = ?`, hashToken(token))
		if n, _ := res.RowsAffected(); err != nil || n != 1 {
			return "", false // used by a parallel request
		}
	}
	return sess, true
}

// protectedDomains asks the helper which app domains have the sign-in in front, and the
// proxy's HTTPS port.
func (a *App) protectedDomains(ctx context.Context) (map[string]bool, int) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	out := map[string]bool{}
	raw, err := a.helper.call(ctx, helperRequest{Action: "proxy_status"}, nil)
	if err != nil {
		return out, 0
	}
	var st struct {
		Routes []proxyRoute
		Config proxyConfig
	}
	json.Unmarshal(raw, &st)
	for _, r := range st.Routes {
		if r.Protect {
			out[r.Domain] = true
		}
	}
	return out, st.Config.HTTPSPort
}

// signinTarget checks the rd address: https, no user info, a protected domain, and no port or
// exactly the proxy's HTTPS port. It returns the address to send the token to, built from the
// checked parts only.
func signinTarget(rd string, protected map[string]bool, httpsPort int) (base, domain, path string, ok bool) {
	u, err := url.Parse(rd)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Host == "" || httpsPort == 0 {
		return "", "", "", false
	}
	domain = strings.ToLower(u.Hostname())
	if !protected[domain] {
		return "", "", "", false
	}
	if p := u.Port(); p != "" && p != strconv.Itoa(httpsPort) {
		return "", "", "", false
	}
	base = "https://" + domain
	if httpsPort != 443 {
		base += ":" + strconv.Itoa(httpsPort)
	}
	return base, domain, safeLocalPath(u.RequestURI()), true
}

func lowerHost(h string) string { return strings.ToLower(hostOnly(h)) }

// safeLocalPath keeps a redirect on the same site: an absolute path, never //host or a scheme.
// Browsers drop tabs and other control characters and read a backslash as a slash, so any of
// them anywhere refuses the path.
func safeLocalPath(p string) string {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.ContainsRune(p, '\\') {
		return "/"
	}
	for i := 0; i < len(p); i++ {
		if p[i] < 0x20 || p[i] == 0x7f {
			return "/"
		}
	}
	if u, err := url.Parse(p); err != nil || u.Scheme != "" || u.Host != "" {
		return "/"
	}
	return p
}

// proxyAuthCheck answers Caddy's forward_auth: 200 lets the request through.
func (a *App) proxyAuthCheck(w http.ResponseWriter, r *http.Request) {
	host := lowerHost(r.Header.Get("X-Forwarded-Host"))
	if c, err := r.Cookie(proxyCookie); err == nil {
		if _, ok := a.store.proxyTokenOK(c.Value, host, "cookie"); ok {
			w.WriteHeader(http.StatusOK)
			return
		}
	}
	box := a.store.setting(proxyBoxURLSave)
	if box == "" {
		http.Error(w, "Sign in to NexoVirt Box first.", http.StatusUnauthorized)
		return
	}
	back := "https://" + host + safeLocalPath(r.Header.Get("X-Forwarded-Uri"))
	http.Redirect(w, r, box+"/api/proxy-auth/start?rd="+url.QueryEscape(back), http.StatusFound)
}

// proxyAuthStart runs on Box's own host: a signed-in user gets a one-time token for the domain.
func (a *App) proxyAuthStart(w http.ResponseWriter, r *http.Request) {
	tok := sessionToken(r)
	if _, ok := a.currentUser(r); !ok || tok == "" {
		http.Redirect(w, r, "/", http.StatusFound) // sign in, then open the app link again
		return
	}
	protected, port := a.protectedDomains(r.Context())
	base, domain, path, ok := signinTarget(r.URL.Query().Get("rd"), protected, port)
	if !ok {
		writeError(w, http.StatusBadRequest, "This address is not an app protected by Box.")
		return
	}
	once := a.store.addProxyToken(domain, hashToken(tok), "once", proxyOnceTTL)
	a.audit(r, "proxy.signin", domain)
	q := url.Values{"t": {once}, "rd": {path}}
	http.Redirect(w, r, base+"/.nxbox-auth?"+q.Encode(), http.StatusFound)
}

// proxyAuthFinish runs on the app's domain (through Caddy): it trades the one-time token for the
// domain cookie.
func (a *App) proxyAuthFinish(w http.ResponseWriter, r *http.Request) {
	host := lowerHost(r.Host)
	sess, ok := a.store.proxyTokenOK(r.URL.Query().Get("t"), host, "once")
	if !ok {
		http.Error(w, "This sign-in link expired. Open the app again.", http.StatusForbidden)
		return
	}
	c := a.store.addProxyToken(host, sess, "cookie", proxyCookieTTL)
	http.SetCookie(w, &http.Cookie{Name: proxyCookie, Value: c, Path: "/", MaxAge: int(proxyCookieTTL.Seconds()),
		Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	http.Redirect(w, r, safeLocalPath(r.URL.Query().Get("rd")), http.StatusFound)
}

// rememberBoxURL keeps the address the admin uses for Box, where protected apps send people to
// sign in.
func (a *App) rememberBoxURL(r *http.Request) {
	if o := r.Header.Get("Origin"); strings.HasPrefix(o, "https://") {
		a.store.setSetting(proxyBoxURLSave, strings.TrimSuffix(o, "/"))
		return
	}
	if r.Host != "" {
		a.store.setSetting(proxyBoxURLSave, "https://"+r.Host)
	}
}
