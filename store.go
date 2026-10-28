package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
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
		);`)
	if err != nil {
		db.Close()
		return nil, err
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
	_, err := s.db.Exec(`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES (?, ?, ?)`,
		hashToken(t), userID, now.Add(sessionTTL).Unix())
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
