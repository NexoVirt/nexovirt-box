package box

// Several boxes in one UI. On the box to be added, the admin creates a pairing token (shown once,
// stored as sha256, scope "full" or "read") and sees the box's certificate fingerprint. On the
// main box the admin adds the other box with its address, the token and (checked) fingerprint.
// The main box then proxies /api/remote/<id>/<path> server side to https://<other>/api/<path>
// with the token, over TLS pinned to that fingerprint (boxes use self-signed certificates); the
// browser only ever talks to its own box, there is no cross-origin request.
//
// A pairing token only reaches the app and system API: sign-in, account, two-step sign-in,
// sessions, the console, pairing and remote boxes themselves are refused (remoteDenied), and
// actions that need a fresh password proof stay refused (a token has no proof). "read" tokens
// allow GET only, without app credentials, compose files and file contents.

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const pairPrefix = "nxbp_"

type ctxKey int

const remoteUserKey ctxKey = 1

// Paths a pairing token never reaches (and the proxy never forwards).
var remoteDeniedPrefixes = []string{
	"/api/auth/", "/api/login", "/api/logout", "/api/setup", "/api/2fa", "/api/account", "/api/sessions",
	"/api/console", "/api/pairing", "/api/remotes", "/api/remote/", "/api/desktop",
}

var readDeniedRe = regexp.MustCompile(`^/api/(apps/[^/]+/(credentials|custom)|files/(download|zip)|backups/key)`)

func remoteDenied(path, method, scope string) bool {
	clean := "/" + strings.TrimLeft(filepath.ToSlash(filepath.Clean("/"+path)), "/")
	if clean != path && clean+"/" != path { // no dot segments or double slashes
		return true
	}
	if !strings.HasPrefix(path, "/api/") {
		return true
	}
	for _, p := range remoteDeniedPrefixes {
		if strings.HasPrefix(path, p) || path+"/" == p {
			return true
		}
	}
	if scope == "read" {
		return (method != http.MethodGet && method != http.MethodHead) || readDeniedRe.MatchString(path)
	}
	return scope != "full"
}

func (s *Store) initRemote() {
	s.db.Exec(`CREATE TABLE IF NOT EXISTS pair_tokens (token_hash TEXT PRIMARY KEY, user_id INTEGER NOT NULL, name TEXT NOT NULL,
		scope TEXT NOT NULL, created INTEGER NOT NULL, last_used INTEGER NOT NULL DEFAULT 0)`)
	s.db.Exec(`CREATE TABLE IF NOT EXISTS remotes (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT NOT NULL, url TEXT NOT NULL,
		token TEXT NOT NULL, fingerprint TEXT NOT NULL, created INTEGER NOT NULL)`)
}

// remoteAuth turns a pairing token into the user who created it, for allowed paths only.
func (a *App) remoteAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		tok, ok := strings.CutPrefix(auth, "Bearer "+pairPrefix)
		if !ok || sessionToken(r) != "" {
			next.ServeHTTP(w, r)
			return
		}
		a.store.initRemote()
		var uid int64
		var scope string
		err := a.store.db.QueryRow(`SELECT user_id, scope FROM pair_tokens WHERE token_hash = ?`, hashToken(pairPrefix+tok)).Scan(&uid, &scope)
		if err != nil {
			if !a.limiter.allow("pair:" + a.clientIP(r)) {
				writeError(w, http.StatusTooManyRequests, "Too many attempts. Wait a minute and try again.")
				return
			}
			writeError(w, http.StatusUnauthorized, "This pairing token is not valid.")
			return
		}
		if remoteDenied(r.URL.Path, r.Method, scope) {
			writeError(w, http.StatusForbidden, "Another box cannot do this here. Sign in to this box directly.")
			return
		}
		a.store.db.Exec(`UPDATE pair_tokens SET last_used = ? WHERE token_hash = ?`, time.Now().Unix(), hashToken(pairPrefix+tok))
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), remoteUserKey, uid)))
	})
}

func remoteUser(r *http.Request) (int64, bool) {
	uid, ok := r.Context().Value(remoteUserKey).(int64)
	return uid, ok
}

// ---- this box: pairing tokens ----

func (a *App) certFingerprint() string {
	b, err := os.ReadFile(filepath.Join(a.state, "cert.pem"))
	if err != nil {
		return ""
	}
	blk, _ := pem.Decode(b)
	if blk == nil {
		return ""
	}
	sum := sha256.Sum256(blk.Bytes)
	return hex.EncodeToString(sum[:])
}

func (a *App) pairingList(w http.ResponseWriter, r *http.Request) {
	a.store.initRemote()
	rows, err := a.store.db.Query(`SELECT substr(token_hash, 1, 12), name, scope, created, last_used FROM pair_tokens ORDER BY created DESC`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "The pairings could not be read.")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, name, scope string
		var created, used int64
		if rows.Scan(&id, &name, &scope, &created, &used) == nil {
			out = append(out, map[string]any{"id": id, "name": name, "scope": scope, "created": created, "last_used": used})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"tokens": out, "fingerprint": a.certFingerprint()})
}

func (a *App) pairingCreate(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name, Scope string }
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !vpnNameRe.MatchString(in.Name) || (in.Scope != "full" && in.Scope != "read") {
		writeError(w, http.StatusUnprocessableEntity, "Give the pairing a name and pick full or read-only access.")
		return
	}
	if !a.needProof(w, r) {
		return
	}
	uid, _ := a.currentUser(r)
	a.store.initRemote()
	tok := pairPrefix + randomToken()
	if _, err := a.store.db.Exec(`INSERT INTO pair_tokens (token_hash, user_id, name, scope, created) VALUES (?, ?, ?, ?, ?)`,
		hashToken(tok), uid, in.Name, in.Scope, time.Now().Unix()); err != nil {
		writeError(w, http.StatusInternalServerError, "The pairing could not be saved.")
		return
	}
	a.audit(r, "pairing.create", in.Name+" ("+in.Scope+")")
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"token": tok, "fingerprint": a.certFingerprint()})
}

func (a *App) pairingRevoke(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if !regexp.MustCompile(`^[0-9a-f]{12}$`).MatchString(id) {
		writeError(w, http.StatusNotFound, "This pairing does not exist.")
		return
	}
	a.store.initRemote()
	res, _ := a.store.db.Exec(`DELETE FROM pair_tokens WHERE substr(token_hash, 1, 12) = ?`, id)
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "This pairing does not exist.")
		return
	}
	a.audit(r, "pairing.revoke", id)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- the main box: remote boxes ----

func (a *App) remoteAEAD() (cipher.AEAD, error) {
	k, err := loadOrCreateKey(filepath.Join(a.state, "remote.key"))
	if err != nil {
		return nil, err
	}
	b, err := aes.NewCipher(k)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(b)
}

func (a *App) sealToken(t string) (string, error) {
	g, err := a.remoteAEAD()
	if err != nil {
		return "", err
	}
	nonce := make([]byte, g.NonceSize())
	rand.Read(nonce)
	return base64.StdEncoding.EncodeToString(g.Seal(nonce, nonce, []byte(t), nil)), nil
}

func (a *App) openToken(s string) string {
	g, err := a.remoteAEAD()
	if err != nil {
		return ""
	}
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil || len(b) < g.NonceSize() {
		return ""
	}
	pt, err := g.Open(nil, b[:g.NonceSize()], b[g.NonceSize():], nil)
	if err != nil {
		return ""
	}
	return string(pt)
}

// pinnedClient trusts exactly the certificate with this sha256 (a box's self-signed one).
func pinnedTransport(fingerprint string) *http.Transport {
	return &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: true, //nolint:gosec // replaced by the pin check below
			VerifyPeerCertificate: func(raw [][]byte, _ [][]*x509.Certificate) error {
				if len(raw) == 0 {
					return errors.New("no certificate")
				}
				sum := sha256.Sum256(raw[0])
				if subtle.ConstantTimeCompare([]byte(hex.EncodeToString(sum[:])), []byte(fingerprint)) != 1 {
					return errors.New("the certificate does not match the pinned fingerprint")
				}
				return nil
			},
		},
		DialContext:           (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		ResponseHeaderTimeout: 60 * time.Second,
	}
}

// remoteBase checks the other box's address: https, a host, no path, user info or query.
func remoteBase(raw string) (*url.URL, error) {
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return nil, uerr("Enter the other box's address like https://192.168.1.20:8443.")
	}
	u.Path = ""
	return u, nil
}

// peerFingerprint reads the certificate the other box presents.
func peerFingerprint(ctx context.Context, host string) (string, error) {
	d := tls.Dialer{Config: &tls.Config{InsecureSkipVerify: true}, NetDialer: &net.Dialer{Timeout: 10 * time.Second}} //nolint:gosec // only to read the cert
	c, err := d.DialContext(ctx, "tcp", host)
	if err != nil {
		return "", uerr("The other box does not answer at this address.")
	}
	defer c.Close()
	certs := c.(*tls.Conn).ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", uerr("The other box sent no certificate.")
	}
	sum := sha256.Sum256(certs[0].Raw)
	return hex.EncodeToString(sum[:]), nil
}

var fingerprintRe = regexp.MustCompile(`^[0-9a-f]{64}$`)

func (a *App) remotesList(w http.ResponseWriter, r *http.Request) {
	a.store.initRemote()
	rows, err := a.store.db.Query(`SELECT id, name, url, fingerprint, created FROM remotes ORDER BY name`)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "The boxes could not be read.")
		return
	}
	defer rows.Close()
	out := []map[string]any{}
	for rows.Next() {
		var id, created int64
		var name, u, fp string
		if rows.Scan(&id, &name, &u, &fp, &created) == nil {
			out = append(out, map[string]any{"id": id, "name": name, "url": u, "fingerprint": fp, "created": created})
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) remoteAdd(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name, URL, Token, Fingerprint string }
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Name, in.Token = strings.TrimSpace(in.Name), strings.TrimSpace(in.Token)
	in.Fingerprint = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(in.Fingerprint), ":", ""))
	base, err := remoteBase(in.URL)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if !vpnNameRe.MatchString(in.Name) || !strings.HasPrefix(in.Token, pairPrefix) || len(in.Token) > 200 || !fingerprintRe.MatchString(in.Fingerprint) {
		writeError(w, http.StatusUnprocessableEntity, "Fill in the name, the pairing token and the certificate fingerprint shown on the other box.")
		return
	}
	if !a.needProof(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	fp, err := peerFingerprint(ctx, base.Host)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	if subtle.ConstantTimeCompare([]byte(fp), []byte(in.Fingerprint)) != 1 {
		writeError(w, http.StatusUnprocessableEntity, "The other box shows a different certificate than the fingerprint you entered. Check the address; someone may be in between.")
		return
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base.String()+"/api/system", nil)
	req.Header.Set("Authorization", "Bearer "+in.Token)
	res, err := (&http.Client{Transport: pinnedTransport(fp), Timeout: 20 * time.Second}).Do(req)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, "The other box could not be reached.")
		return
	}
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		writeError(w, http.StatusUnprocessableEntity, "The other box refused the pairing token.")
		return
	}
	sealed, err := a.sealToken(in.Token)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "The token could not be stored.")
		return
	}
	a.store.initRemote()
	if _, err := a.store.db.Exec(`INSERT INTO remotes (name, url, token, fingerprint, created) VALUES (?, ?, ?, ?, ?)`,
		in.Name, base.String(), sealed, fp, time.Now().Unix()); err != nil {
		writeError(w, http.StatusInternalServerError, "The box could not be saved.")
		return
	}
	a.audit(r, "remote.add", in.Name+" "+base.String())
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (a *App) remoteDelete(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "This box does not exist.")
		return
	}
	a.store.initRemote()
	res, _ := a.store.db.Exec(`DELETE FROM remotes WHERE id = ?`, id)
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "This box does not exist.")
		return
	}
	a.audit(r, "remote.delete", strconv.FormatInt(id, 10))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// remoteProxy forwards /api/remote/<id>/<rest> to the other box's /api/<rest>.
func (a *App) remoteProxy(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "This box does not exist.")
		return
	}
	path := "/api/" + r.PathValue("rest")
	if remoteDenied(path, r.Method, "full") {
		writeError(w, http.StatusForbidden, "This is only available on the box itself.")
		return
	}
	a.store.initRemote()
	var u, sealed, fp string
	if a.store.db.QueryRow(`SELECT url, token, fingerprint FROM remotes WHERE id = ?`, id).Scan(&u, &sealed, &fp) != nil {
		writeError(w, http.StatusNotFound, "This box does not exist.")
		return
	}
	tok := a.openToken(sealed)
	base, err := url.Parse(u)
	if err != nil || tok == "" {
		writeError(w, http.StatusBadGateway, "The stored pairing of this box is damaged. Remove and add it again.")
		return
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host, pr.Out.Host = base.Scheme, base.Host, base.Host
			pr.Out.URL.Path, pr.Out.URL.RawPath = path, ""
			pr.Out.URL.RawQuery = r.URL.RawQuery
			for _, h := range []string{"Cookie", "Origin", "Referer", "Authorization", "X-Forwarded-For", "X-Real-Ip"} {
				pr.Out.Header.Del(h)
			}
			pr.Out.Header.Set("Authorization", "Bearer "+tok)
		},
		Transport:     pinnedTransport(fp),
		FlushInterval: -1, // the stats stream
		ModifyResponse: func(res *http.Response) error {
			res.Header.Del("Set-Cookie")
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeError(w, http.StatusBadGateway, "The other box does not answer.")
		},
	}
	r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
	rp.ServeHTTP(w, r)
}
