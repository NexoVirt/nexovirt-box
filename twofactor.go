package main

// Two-factor sign-in: TOTP (authenticator app) with single-use recovery codes, and passkeys
// (WebAuthn). Turning either on needs the current password. With either on, a correct password
// only starts a pending sign-in that the second factor completes.

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/skip2/go-qrcode"
)

const pendingTTL = 5 * time.Minute

type pendingLogin struct {
	uid      int64
	exp      time.Time
	fails    int
	passkey  *webauthn.SessionData
	username string
}

// twoFactor holds short-lived state in memory (pending sign-ins, setup secrets, WebAuthn
// ceremonies). A restart only means starting that step again.
type twoFactor struct {
	mu       sync.Mutex
	pending  map[string]*pendingLogin         // pending token -> sign-in
	totpNew  map[string]string                // session hash -> secret being set up
	regs     map[string]*webauthn.SessionData // session hash -> passkey registration
	regsUser map[string]int64
}

func newTwoFactor() *twoFactor {
	return &twoFactor{pending: map[string]*pendingLogin{}, totpNew: map[string]string{}, regs: map[string]*webauthn.SessionData{}, regsUser: map[string]int64{}}
}

// ---- store ----

func (s *Store) totpSecret(uid int64) string {
	var v string
	s.db.QueryRow(`SELECT COALESCE(totp_secret, '') FROM users WHERE id = ?`, uid).Scan(&v)
	return v
}

func (s *Store) setTOTP(uid int64, secret string) error {
	var v any
	if secret != "" {
		v = secret
	}
	_, err := s.db.Exec(`UPDATE users SET totp_secret = ? WHERE id = ?`, v, uid)
	return err
}

type passkeyRow struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Created  int64  `json:"created_at"`
	LastUsed int64  `json:"last_used_at"`
	cred     webauthn.Credential
}

func (s *Store) passkeys(uid int64) ([]passkeyRow, error) {
	rows, err := s.db.Query(`SELECT id, name, credential, created_at, last_used_at FROM passkeys WHERE user_id = ? ORDER BY created_at`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []passkeyRow{}
	for rows.Next() {
		var p passkeyRow
		var raw string
		if err := rows.Scan(&p.ID, &p.Name, &raw, &p.Created, &p.LastUsed); err != nil {
			return nil, err
		}
		if json.Unmarshal([]byte(raw), &p.cred) != nil {
			continue
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) savePasskey(uid int64, name string, c *webauthn.Credential) error {
	raw, _ := json.Marshal(c)
	now := time.Now().Unix()
	_, err := s.db.Exec(`INSERT INTO passkeys (id, user_id, name, credential, created_at, last_used_at) VALUES (?, ?, ?, ?, ?, 0)
		ON CONFLICT(id) DO UPDATE SET credential = excluded.credential, last_used_at = ?`,
		base64.RawURLEncoding.EncodeToString(c.ID), uid, name, string(raw), now, now)
	return err
}

func (s *Store) deletePasskey(uid int64, id string) (bool, error) {
	res, err := s.db.Exec(`DELETE FROM passkeys WHERE user_id = ? AND id = ?`, uid, id)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

func (s *Store) newRecoveryCodes(uid int64) ([]string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM recovery_codes WHERE user_id = ?`, uid); err != nil {
		return nil, err
	}
	codes := make([]string, 10)
	for i := range codes {
		b := make([]byte, 6)
		rand.Read(b)
		c := strings.ToLower(b32.EncodeToString(b)) // 10 characters
		codes[i] = c[:5] + "-" + c[5:]
		if _, err := tx.Exec(`INSERT INTO recovery_codes (code_hash, user_id) VALUES (?, ?)`, hashToken(codes[i]), uid); err != nil {
			return nil, err
		}
	}
	return codes, tx.Commit()
}

func (s *Store) recoveryLeft(uid int64) int {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM recovery_codes WHERE user_id = ?`, uid).Scan(&n)
	return n
}

// useRecoveryCode deletes the code when it matches (single use).
func (s *Store) useRecoveryCode(uid int64, code string) bool {
	code = strings.ToLower(strings.TrimSpace(code))
	res, err := s.db.Exec(`DELETE FROM recovery_codes WHERE user_id = ? AND code_hash = ?`, uid, hashToken(code))
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

func (s *Store) has2FA(uid int64) (totp bool, passkeys bool) {
	var n int
	s.db.QueryRow(`SELECT COUNT(*) FROM passkeys WHERE user_id = ?`, uid).Scan(&n)
	return s.totpSecret(uid) != "", n > 0
}

// ---- WebAuthn plumbing ----

type boxUser struct {
	id    int64
	name  string
	creds []webauthn.Credential
}

func (u boxUser) WebAuthnID() []byte                         { return []byte(fmt.Sprintf("nexovirt-box-user-%d", u.id)) }
func (u boxUser) WebAuthnName() string                       { return u.name }
func (u boxUser) WebAuthnDisplayName() string                { return u.name }
func (u boxUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func (a *App) loadBoxUser(uid int64) (boxUser, error) {
	rows, err := a.store.passkeys(uid)
	if err != nil {
		return boxUser{}, err
	}
	u := boxUser{id: uid, name: a.store.username(uid)}
	for _, p := range rows {
		u.creds = append(u.creds, p.cred)
	}
	return u, nil
}

var errPasskeyHost = userError{"Passkeys need a domain name with a trusted certificate (or localhost). Open NexoVirt Box by its name to use one."}

// webAuthnFor builds the relying party for the host the browser used. Browsers refuse passkeys
// on IP addresses, so those are refused here with a clear message.
func (a *App) webAuthnFor(r *http.Request) (*webauthn.WebAuthn, error) {
	host := r.Host
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "" || net.ParseIP(strings.Trim(host, "[]")) != nil {
		return nil, errPasskeyHost
	}
	scheme := "https"
	if !a.secure {
		scheme = "http"
	}
	return webauthn.New(&webauthn.Config{
		RPID:          host,
		RPDisplayName: "NexoVirt Box",
		RPOrigins:     []string{scheme + "://" + r.Host},
	})
}

// ---- handlers: settings side ----

func readJSON(r *http.Request, v any) error {
	r.Body = http.MaxBytesReader(nil, r.Body, 64<<10)
	if json.NewDecoder(r.Body).Decode(v) != nil {
		return userError{"The request could not be read."}
	}
	return nil
}

func fail(w http.ResponseWriter, err error) {
	var ue userError
	if errors.As(err, &ue) {
		writeError(w, http.StatusUnprocessableEntity, ue.msg)
		return
	}
	log.Printf("2fa: %v", err)
	writeError(w, http.StatusInternalServerError, "That did not work. Check the service log.")
}

// checkPassword verifies the current password of the signed-in user.
func (a *App) checkPassword(r *http.Request, password string) (int64, error) {
	uid, _ := a.currentUser(r)
	if !a.limiter.allow("pw:" + a.clientIP(r)) {
		return 0, userError{"Too many attempts. Wait a minute and try again."}
	}
	if _, ok := a.store.checkLogin(a.store.username(uid), password); !ok {
		a.audit(r, "2fa.password_failed", "")
		return 0, userError{"The password is wrong."}
	}
	return uid, nil
}

func (a *App) twoFactorStatus(w http.ResponseWriter, r *http.Request) {
	uid, _ := a.currentUser(r)
	keys, err := a.store.passkeys(uid)
	if err != nil {
		fail(w, err)
		return
	}
	_, perr := a.webAuthnFor(r)
	writeJSON(w, http.StatusOK, map[string]any{
		"totp": a.store.totpSecret(uid) != "", "passkeys": keys, "recovery_left": a.store.recoveryLeft(uid),
		"passkeys_available": perr == nil,
	})
}

func qrDataURI(text string) (string, error) {
	q, err := qrcode.New(text, qrcode.Medium)
	if err != nil {
		return "", err
	}
	q.DisableBorder = false
	bm := q.Bitmap()
	var path strings.Builder
	for y, row := range bm {
		for x, on := range row {
			if on {
				fmt.Fprintf(&path, "M%d %dh1v1h-1z", x, y)
			}
		}
	}
	n := len(bm)
	svg := fmt.Sprintf(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 %d %d" shape-rendering="crispEdges"><rect width="100%%" height="100%%" fill="#fff"/><path fill="#000" d="%s"/></svg>`, n, n, path.String())
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg)), nil
}

func (a *App) totpBegin(w http.ResponseWriter, r *http.Request) {
	var in struct{ Password string }
	if err := readJSON(r, &in); err != nil {
		fail(w, err)
		return
	}
	uid, err := a.checkPassword(r, in.Password)
	if err != nil {
		fail(w, err)
		return
	}
	secret := newTOTPSecret()
	uri := totpURI("NexoVirt Box", a.store.username(uid), secret)
	qr, err := qrDataURI(uri)
	if err != nil {
		fail(w, err)
		return
	}
	a.tfa.mu.Lock()
	a.tfa.totpNew[hashToken(sessionToken(r))] = secret
	a.tfa.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]string{"secret": secret, "uri": uri, "qr": qr})
}

func (a *App) totpEnable(w http.ResponseWriter, r *http.Request) {
	var in struct{ Code string }
	if err := readJSON(r, &in); err != nil {
		fail(w, err)
		return
	}
	uid, _ := a.currentUser(r)
	key := hashToken(sessionToken(r))
	a.tfa.mu.Lock()
	secret := a.tfa.totpNew[key]
	a.tfa.mu.Unlock()
	if secret == "" {
		fail(w, userError{"Start the setup again."})
		return
	}
	if !totpValid(secret, in.Code, time.Now()) {
		fail(w, userError{"The code is wrong. Check the time on your phone and try the newest code."})
		return
	}
	if err := a.store.setTOTP(uid, secret); err != nil {
		fail(w, err)
		return
	}
	a.tfa.mu.Lock()
	delete(a.tfa.totpNew, key)
	a.tfa.mu.Unlock()
	a.proofs.Delete(key) // the console now also needs the code
	a.audit(r, "2fa.totp_on", "")
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": a.firstRecoveryCodes(uid)})
}

// firstRecoveryCodes creates recovery codes the first time a second factor is turned on.
func (a *App) firstRecoveryCodes(uid int64) []string {
	if a.store.recoveryLeft(uid) > 0 {
		return nil
	}
	codes, err := a.store.newRecoveryCodes(uid)
	if err != nil {
		log.Printf("recovery codes: %v", err)
	}
	return codes
}

func (a *App) totpDisable(w http.ResponseWriter, r *http.Request) {
	var in struct{ Password, Code string }
	if err := readJSON(r, &in); err != nil {
		fail(w, err)
		return
	}
	uid, err := a.checkPassword(r, in.Password)
	if err != nil {
		fail(w, err)
		return
	}
	if !totpValid(a.store.totpSecret(uid), in.Code, time.Now()) {
		fail(w, userError{"The code from your authenticator app is wrong."})
		return
	}
	if err := a.store.setTOTP(uid, ""); err != nil {
		fail(w, err)
		return
	}
	a.audit(r, "2fa.totp_off", "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (a *App) recoveryRegenerate(w http.ResponseWriter, r *http.Request) {
	var in struct{ Password string }
	if err := readJSON(r, &in); err != nil {
		fail(w, err)
		return
	}
	uid, err := a.checkPassword(r, in.Password)
	if err != nil {
		fail(w, err)
		return
	}
	codes, err := a.store.newRecoveryCodes(uid)
	if err != nil {
		fail(w, err)
		return
	}
	a.audit(r, "2fa.recovery_new", "")
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
}

func (a *App) passkeyBegin(w http.ResponseWriter, r *http.Request) {
	var in struct{ Password string }
	if err := readJSON(r, &in); err != nil {
		fail(w, err)
		return
	}
	wa, err := a.webAuthnFor(r)
	if err != nil {
		fail(w, err)
		return
	}
	uid, err := a.checkPassword(r, in.Password)
	if err != nil {
		fail(w, err)
		return
	}
	u, err := a.loadBoxUser(uid)
	if err != nil {
		fail(w, err)
		return
	}
	exclude := make([]protocol.CredentialDescriptor, 0, len(u.creds))
	for _, c := range u.creds {
		exclude = append(exclude, c.Descriptor())
	}
	creation, sess, err := wa.BeginRegistration(u,
		webauthn.WithExclusions(exclude),
		webauthn.WithResidentKeyRequirement(protocol.ResidentKeyRequirementPreferred))
	if err != nil {
		fail(w, err)
		return
	}
	key := hashToken(sessionToken(r))
	a.tfa.mu.Lock()
	a.tfa.regs[key], a.tfa.regsUser[key] = sess, uid
	a.tfa.mu.Unlock()
	writeJSON(w, http.StatusOK, creation)
}

func (a *App) passkeyFinish(w http.ResponseWriter, r *http.Request) {
	wa, err := a.webAuthnFor(r)
	if err != nil {
		fail(w, err)
		return
	}
	key := hashToken(sessionToken(r))
	a.tfa.mu.Lock()
	sess, uid := a.tfa.regs[key], a.tfa.regsUser[key]
	delete(a.tfa.regs, key)
	delete(a.tfa.regsUser, key)
	a.tfa.mu.Unlock()
	if sess == nil {
		fail(w, userError{"Start adding the passkey again."})
		return
	}
	u, err := a.loadBoxUser(uid)
	if err != nil {
		fail(w, err)
		return
	}
	r.Body = http.MaxBytesReader(nil, r.Body, 64<<10)
	cred, err := wa.FinishRegistration(u, *sess, r)
	if err != nil {
		log.Printf("passkey registration: %v", err)
		fail(w, userError{"The passkey could not be added. Try again."})
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" || len(name) > 60 {
		name = "Passkey"
	}
	if err := a.store.savePasskey(uid, name, cred); err != nil {
		fail(w, err)
		return
	}
	a.audit(r, "2fa.passkey_add", name)
	writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": a.firstRecoveryCodes(uid)})
}

func (a *App) passkeyDelete(w http.ResponseWriter, r *http.Request) {
	var in struct{ Password string }
	if err := readJSON(r, &in); err != nil {
		fail(w, err)
		return
	}
	uid, err := a.checkPassword(r, in.Password)
	if err != nil {
		fail(w, err)
		return
	}
	ok, err := a.store.deletePasskey(uid, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if !ok {
		writeError(w, http.StatusNotFound, "This passkey does not exist.")
		return
	}
	a.audit(r, "2fa.passkey_remove", "")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// ---- handlers: sign-in side ----

// startPending is called by login after the password matched for a user with 2FA on.
func (a *App) startPending(uid int64, username string) (string, []string) {
	tok := randomToken()
	a.tfa.mu.Lock()
	now := time.Now()
	for k, p := range a.tfa.pending {
		if now.After(p.exp) {
			delete(a.tfa.pending, k)
		}
	}
	a.tfa.pending[hashToken(tok)] = &pendingLogin{uid: uid, exp: now.Add(pendingTTL), username: username}
	a.tfa.mu.Unlock()
	totp, keys := a.store.has2FA(uid)
	var methods []string
	if totp {
		methods = append(methods, "totp")
	}
	if keys {
		methods = append(methods, "passkey")
	}
	return tok, methods
}

func (a *App) getPending(tok string) *pendingLogin {
	a.tfa.mu.Lock()
	defer a.tfa.mu.Unlock()
	p := a.tfa.pending[hashToken(tok)]
	if p == nil || time.Now().After(p.exp) || p.fails >= 5 {
		return nil
	}
	return p
}

// takeTry counts one try against the pending sign-in BEFORE the code is checked (atomic under
// the lock, so parallel guesses cannot exceed 5); a correct code ends the pending sign-in.
func (a *App) takeTry(tok string) *pendingLogin {
	a.tfa.mu.Lock()
	defer a.tfa.mu.Unlock()
	p := a.tfa.pending[hashToken(tok)]
	if p == nil || time.Now().After(p.exp) || p.fails >= 5 {
		return nil
	}
	p.fails++
	return p
}

func (a *App) endPending(tok string) {
	a.tfa.mu.Lock()
	delete(a.tfa.pending, hashToken(tok))
	a.tfa.mu.Unlock()
}

func (a *App) finishLogin(w http.ResponseWriter, r *http.Request, p *pendingLogin, tok, how string) {
	a.endPending(tok)
	a.store.audit(p.username, a.clientIP(r), "login.ok", how)
	if err := a.startSession(w, r, p.uid); err != nil {
		log.Printf("login session: %v", err)
		writeError(w, http.StatusInternalServerError, "You could not be signed in. Check the service log.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

var errPendingGone = userError{"The sign-in took too long or had too many wrong codes. Start again with your password."}

func (a *App) login2FA(w http.ResponseWriter, r *http.Request) {
	var in struct{ Pending, Code string }
	if err := readJSON(r, &in); err != nil {
		fail(w, err)
		return
	}
	p := a.takeTry(in.Pending)
	if p == nil {
		writeError(w, http.StatusUnauthorized, errPendingGone.msg)
		return
	}
	code := strings.TrimSpace(in.Code)
	switch {
	case len(strings.ReplaceAll(code, " ", "")) == 6 && totpValid(a.store.totpSecret(p.uid), code, time.Now()):
		a.finishLogin(w, r, p, in.Pending, "totp")
	case strings.Contains(code, "-") && a.store.useRecoveryCode(p.uid, code):
		a.finishLogin(w, r, p, in.Pending, "recovery code")
	default:
		a.store.audit(p.username, a.clientIP(r), "login.failed", "wrong 2FA code")
		writeError(w, http.StatusUnauthorized, "The code is wrong.")
	}
}

func (a *App) loginPasskeyBegin(w http.ResponseWriter, r *http.Request) {
	var in struct{ Pending string }
	if err := readJSON(r, &in); err != nil {
		fail(w, err)
		return
	}
	p := a.getPending(in.Pending)
	if p == nil {
		writeError(w, http.StatusUnauthorized, errPendingGone.msg)
		return
	}
	wa, err := a.webAuthnFor(r)
	if err != nil {
		fail(w, err)
		return
	}
	u, err := a.loadBoxUser(p.uid)
	if err != nil || len(u.creds) == 0 {
		fail(w, userError{"This account has no passkey."})
		return
	}
	assertion, sess, err := wa.BeginLogin(u)
	if err != nil {
		fail(w, err)
		return
	}
	a.tfa.mu.Lock()
	p.passkey = sess
	a.tfa.mu.Unlock()
	writeJSON(w, http.StatusOK, assertion)
}

func (a *App) loginPasskeyFinish(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("pending")
	p := a.takeTry(tok)
	if p == nil || p.passkey == nil {
		writeError(w, http.StatusUnauthorized, errPendingGone.msg)
		return
	}
	wa, err := a.webAuthnFor(r)
	if err != nil {
		fail(w, err)
		return
	}
	u, err := a.loadBoxUser(p.uid)
	if err != nil {
		fail(w, err)
		return
	}
	r.Body = http.MaxBytesReader(nil, r.Body, 64<<10)
	cred, err := wa.FinishLogin(u, *p.passkey, r)
	if err != nil {
		log.Printf("passkey sign-in: %v", err)
		a.store.audit(p.username, a.clientIP(r), "login.failed", "passkey")
		writeError(w, http.StatusUnauthorized, "The passkey was not accepted.")
		return
	}
	// Keep the sign count (clone detection) and last use.
	for _, old := range u.creds {
		if bytes.Equal(old.ID, cred.ID) {
			a.store.savePasskey(p.uid, "", cred)
		}
	}
	a.finishLogin(w, r, p, tok, "passkey")
}
