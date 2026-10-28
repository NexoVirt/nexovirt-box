package main

// Console proofs. Opening the root console needs a password entered in the last few minutes.
// The helper checks that itself, so a compromised web process cannot open a console on its own:
// the web process forwards the password (and a 2FA code when enabled), the helper compares it
// with the hash in the web database and answers with a short-lived proof, an HMAC under a key
// only the helper holds (random per helper start). The console action refuses without a valid
// proof that is bound to a session that still exists.

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const proofTTL = 5 * time.Minute

var errProof = userError{"Enter your password to open the console."}

type proofKeeper struct {
	key      []byte
	mu       sync.Mutex
	attempts []time.Time // failed password checks, for the helper's own rate limit
}

func newProofKeeper() *proofKeeper {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		panic(err)
	}
	return &proofKeeper{key: k}
}

func (p *proofKeeper) mac(session string, exp int64) string {
	m := hmac.New(sha256.New, p.key)
	fmt.Fprintf(m, "console|%s|%d", session, exp)
	return hex.EncodeToString(m.Sum(nil))
}

func (p *proofKeeper) issue(session string, now time.Time) string {
	exp := now.Add(proofTTL).Unix()
	return strconv.FormatInt(exp, 10) + "." + p.mac(session, exp)
}

func (p *proofKeeper) valid(proof, session string, now time.Time) bool {
	expS, mac, ok := strings.Cut(proof, ".")
	if !ok || session == "" {
		return false
	}
	exp, err := strconv.ParseInt(expS, 10, 64)
	if err != nil || now.Unix() >= exp {
		return false
	}
	return hmac.Equal([]byte(mac), []byte(p.mac(session, exp)))
}

// allowTry is the helper's own limit (5 failed checks per minute), independent of the web one.
func (p *proofKeeper) allowTry(now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	kept := p.attempts[:0]
	for _, t := range p.attempts {
		if now.Sub(t) < time.Minute {
			kept = append(kept, t)
		}
	}
	p.attempts = kept
	return len(p.attempts) < 5
}

func (p *proofKeeper) failed(now time.Time) {
	p.mu.Lock()
	p.attempts = append(p.attempts, now)
	p.mu.Unlock()
}

// webDB opens the web database read only (never creating files there as root).
func (h *helperServer) webDB() (*sql.DB, error) {
	return sql.Open("sqlite", "file:"+filepath.Join(h.webState, "box.db")+"?mode=ro&_pragma=busy_timeout(5000)")
}

// sessionAlive reports whether the session (sha256 of the cookie) still exists.
func sessionAlive(db *sql.DB, session string, now time.Time) bool {
	var n int
	err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token_hash = ? AND expires_at > ?`, session, now.Unix()).Scan(&n)
	return err == nil && n == 1
}

// consoleProof checks the password (and 2FA code when enabled) against the web database and
// returns a proof for the session.
func (h *helperServer) consoleProof(req helperRequest, _ func(string)) (any, error) {
	now := time.Now()
	if !h.proofs.allowTry(now) {
		return nil, uerr("Too many attempts. Wait a minute and try again.")
	}
	db, err := h.webDB()
	if err != nil {
		return nil, err
	}
	defer db.Close()
	if !sessionAlive(db, req.Session, now) {
		return nil, uerr("Please sign in again.")
	}
	var hash, totpSecret string
	if err := db.QueryRow(`SELECT u.password_hash, COALESCE(u.totp_secret, '') FROM users u JOIN sessions s ON s.user_id = u.id WHERE s.token_hash = ?`, req.Session).Scan(&hash, &totpSecret); err != nil {
		return nil, err
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(req.Password)) != nil {
		h.proofs.failed(now)
		return nil, uerr("The password is wrong.")
	}
	if totpSecret != "" && !totpValid(totpSecret, req.Code, now) {
		h.proofs.failed(now)
		return nil, uerr("The code from your authenticator app is wrong.")
	}
	return map[string]string{"proof": h.proofs.issue(req.Session, now)}, nil
}

// consoleAllowed is checked before a shell starts.
func (h *helperServer) consoleAllowed(req helperRequest) bool {
	now := time.Now()
	if !h.proofs.valid(req.Proof, req.Session, now) {
		return false
	}
	db, err := h.webDB()
	if err != nil {
		return false
	}
	defer db.Close()
	return sessionAlive(db, req.Session, now)
}
