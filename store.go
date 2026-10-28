package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log"
	"os"
	"strings"
	"time"

	"golang.org/x/crypto/bcrypt"
	_ "modernc.org/sqlite"
)

const sessionTTL = 24 * time.Hour

var errUserExists = errors.New("an account already exists")

type Store struct{ db *sql.DB }

var dummyHash, _ = bcrypt.GenerateFromPassword([]byte("nexovirt-box-dummy"), bcrypt.DefaultCost)

func openStore(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	// The CHECK on users.id makes "only one admin" a database rule: a second insert fails.
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			username TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS sessions (
			token_hash TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL,
			expires_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS attempts (key TEXT NOT NULL, at INTEGER NOT NULL);
		CREATE INDEX IF NOT EXISTS attempts_key ON attempts (key, at);
		CREATE TABLE IF NOT EXISTS audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			at INTEGER NOT NULL,
			username TEXT NOT NULL,
			ip TEXT NOT NULL,
			action TEXT NOT NULL,
			detail TEXT NOT NULL
		);`)
	if err == nil {
		// Added after 0.1: when the session last proved the password (sign-in or re-entry).
		if _, e := db.Exec(`ALTER TABLE sessions ADD COLUMN reauth_at INTEGER NOT NULL DEFAULT 0`); e != nil && !strings.Contains(e.Error(), "duplicate column") {
			err = e
		}
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	// The database holds password hashes and session hashes: owner only.
	for _, f := range []string{path, path + "-wal", path + "-shm"} {
		if _, e := os.Stat(f); e == nil {
			os.Chmod(f, 0o600)
		}
	}
	return &Store{db: db}, nil
}

func (s *Store) hasUser() (bool, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n)
	return n > 0, err
}

func (s *Store) createUser(username, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO users (id, username, password_hash, created_at) VALUES (1, ?, ?, ?)`,
		username, string(hash), time.Now().Unix())
	if err != nil {
		if ok, _ := s.hasUser(); ok {
			return errUserExists
		}
	}
	return err
}

// checkLogin compares in constant work whether or not the user exists.
func (s *Store) checkLogin(username, password string) (int64, bool) {
	var id int64
	var name, hash string
	err := s.db.QueryRow(`SELECT id, username, password_hash FROM users WHERE id = 1`).Scan(&id, &name, &hash)
	if err != nil {
		hash = string(dummyHash) // keeps the timing equal when no account exists
	}
	okPass := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	return id, err == nil && okPass && name == username
}

func (s *Store) username(id int64) string {
	var name string
	s.db.QueryRow(`SELECT username FROM users WHERE id = ?`, id).Scan(&name)
	return name
}

func hashToken(t string) string {
	sum := sha256.Sum256([]byte(t))
	return hex.EncodeToString(sum[:])
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func (s *Store) newSession(userID int64) (string, error) {
	t := randomToken()
	now := time.Now()
	s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now.Unix())
	_, err := s.db.Exec(`INSERT INTO sessions (token_hash, user_id, expires_at, reauth_at) VALUES (?, ?, ?, ?)`,
		hashToken(t), userID, now.Add(sessionTTL).Unix(), now.Unix())
	return t, err
}

func (s *Store) sessionUser(token string) (int64, bool) {
	if token == "" {
		return 0, false
	}
	var id int64
	err := s.db.QueryRow(`SELECT user_id FROM sessions WHERE token_hash = ? AND expires_at > ?`,
		hashToken(token), time.Now().Unix()).Scan(&id)
	return id, err == nil
}

func (s *Store) deleteSession(token string) {
	s.db.Exec(`DELETE FROM sessions WHERE token_hash = ?`, hashToken(token))
}

func (s *Store) markReauth(token string) {
	s.db.Exec(`UPDATE sessions SET reauth_at = ? WHERE token_hash = ?`, time.Now().Unix(), hashToken(token))
}

// sessionFresh reports whether the session proved the password within the window.
func (s *Store) sessionFresh(token string, within time.Duration) bool {
	if token == "" {
		return false
	}
	var at int64
	err := s.db.QueryRow(`SELECT reauth_at FROM sessions WHERE token_hash = ? AND expires_at > ?`,
		hashToken(token), time.Now().Unix()).Scan(&at)
	return err == nil && time.Since(time.Unix(at, 0)) < within
}

// attempt records one try for key and reports whether it is within max per window.
func (s *Store) attempt(key string, max int, window time.Duration) (bool, error) {
	now := time.Now()
	cutoff := now.Add(-window).Unix()
	if _, err := s.db.Exec(`DELETE FROM attempts WHERE at <= ?`, cutoff); err != nil {
		return false, err
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM attempts WHERE key = ? AND at > ?`, key, cutoff).Scan(&n); err != nil {
		return false, err
	}
	if n >= max {
		return false, nil
	}
	_, err := s.db.Exec(`INSERT INTO attempts (key, at) VALUES (?, ?)`, key, now.Unix())
	return err == nil, err
}

type AuditEntry struct {
	At       int64  `json:"at"`
	Username string `json:"username"`
	IP       string `json:"ip"`
	Action   string `json:"action"`
	Detail   string `json:"detail"`
}

// audit appends one entry; the log keeps the newest 5000.
func (s *Store) audit(username, ip, action, detail string) {
	if _, err := s.db.Exec(`INSERT INTO audit (at, username, ip, action, detail) VALUES (?, ?, ?, ?, ?)`,
		time.Now().Unix(), username, ip, action, detail); err != nil {
		log.Printf("audit: %v", err)
		return
	}
	s.db.Exec(`DELETE FROM audit WHERE id <= (SELECT MAX(id) - 5000 FROM audit)`)
}

func (s *Store) recentAudit(limit int) ([]AuditEntry, error) {
	rows, err := s.db.Query(`SELECT at, username, ip, action, detail FROM audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.At, &e.Username, &e.IP, &e.Action, &e.Detail); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
