package main

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
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
		CREATE TABLE IF NOT EXISTS recovery_codes (code_hash TEXT PRIMARY KEY, user_id INTEGER NOT NULL);
		CREATE TABLE IF NOT EXISTS passkeys (
			id TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL,
			name TEXT NOT NULL,
			credential TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			last_used_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL,
			app TEXT NOT NULL,
			status TEXT NOT NULL,
			started INTEGER NOT NULL,
			finished INTEGER NOT NULL DEFAULT 0,
			error TEXT NOT NULL DEFAULT '',
			lines TEXT NOT NULL DEFAULT '[]',
			result TEXT NOT NULL DEFAULT 'null'
		);
		CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS backup_schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			app TEXT NOT NULL,
			target TEXT NOT NULL,
			freq TEXT NOT NULL,
			weekday INTEGER NOT NULL DEFAULT 0,
			time TEXT NOT NULL,
			keep TEXT NOT NULL DEFAULT '{}',
			enabled INTEGER NOT NULL DEFAULT 1,
			last_run INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS backup_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			at INTEGER NOT NULL,
			kind TEXT NOT NULL,
			app TEXT NOT NULL,
			target TEXT NOT NULL,
			target_name TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			size INTEGER NOT NULL DEFAULT 0,
			seconds INTEGER NOT NULL DEFAULT 0,
			status TEXT NOT NULL,
			error TEXT NOT NULL DEFAULT '',
			source TEXT NOT NULL DEFAULT 'manual'
		);
		CREATE TABLE IF NOT EXISTS audit (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			at INTEGER NOT NULL,
			username TEXT NOT NULL,
			ip TEXT NOT NULL,
			action TEXT NOT NULL,
			detail TEXT NOT NULL
		);
		CREATE TABLE IF NOT EXISTS share_links (
			id TEXT PRIMARY KEY,
			token_hash TEXT NOT NULL UNIQUE,
			path TEXT NOT NULL,
			name TEXT NOT NULL,
			is_dir INTEGER NOT NULL,
			created_at INTEGER NOT NULL,
			expires_at INTEGER NOT NULL,
			password_hash TEXT NOT NULL DEFAULT '',
			max_downloads INTEGER NOT NULL DEFAULT 0,
			downloads INTEGER NOT NULL DEFAULT 0,
			revoked INTEGER NOT NULL DEFAULT 0
		);`)
	// Columns added after 0.1 (ALTER is a no-op when the column exists).
	for _, alter := range []string{
		`ALTER TABLE sessions ADD COLUMN reauth_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE users ADD COLUMN totp_secret TEXT`,
		`ALTER TABLE sessions ADD COLUMN created_at INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE sessions ADD COLUMN ip TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE sessions ADD COLUMN agent TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE users ADD COLUMN desktop TEXT NOT NULL DEFAULT '{}'`,
	} {
		if err != nil {
			break
		}
		if _, e := db.Exec(alter); e != nil && !strings.Contains(e.Error(), "duplicate column") {
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

// desktop returns the user's desktop layout as stored (JSON, validated on save).
func (s *Store) desktop(id int64) string {
	var v string
	s.db.QueryRow(`SELECT desktop FROM users WHERE id = ?`, id).Scan(&v)
	return v
}

func (s *Store) setDesktop(id int64, v string) error {
	_, err := s.db.Exec(`UPDATE users SET desktop = ? WHERE id = ?`, v, id)
	return err
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

func (s *Store) newSession(userID int64, ip, agent string) (string, error) {
	t := randomToken()
	now := time.Now()
	if len(agent) > 200 {
		agent = agent[:200]
	}
	s.db.Exec(`DELETE FROM sessions WHERE expires_at < ?`, now.Unix())
	_, err := s.db.Exec(`INSERT INTO sessions (token_hash, user_id, expires_at, reauth_at, created_at, ip, agent) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		hashToken(t), userID, now.Add(sessionTTL).Unix(), now.Unix(), now.Unix(), ip, agent)
	return t, err
}

// SessionInfo is a signed-in browser as Settings shows it. ID is a short, non-secret label.
type SessionInfo struct {
	ID        string `json:"id"`
	CreatedAt int64  `json:"created_at"`
	IP        string `json:"ip"`
	Agent     string `json:"agent"`
	Current   bool   `json:"current"`
}

func (s *Store) sessionsFor(uid int64, currentHash string) []SessionInfo {
	out := []SessionInfo{}
	rows, err := s.db.Query(`SELECT token_hash, created_at, ip, agent FROM sessions WHERE user_id = ? AND expires_at > ? ORDER BY created_at DESC`, uid, time.Now().Unix())
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var h string
		var si SessionInfo
		if rows.Scan(&h, &si.CreatedAt, &si.IP, &si.Agent) == nil {
			si.ID, si.Current = h[:12], h == currentHash
			out = append(out, si)
		}
	}
	return out
}

// endOtherSessions signs out every other browser of the user and returns their hashes.
func (s *Store) endOtherSessions(uid int64, keepHash string) []string {
	var gone []string
	rows, err := s.db.Query(`SELECT token_hash FROM sessions WHERE user_id = ? AND token_hash != ?`, uid, keepHash)
	if err == nil {
		for rows.Next() {
			var h string
			if rows.Scan(&h) == nil {
				gone = append(gone, h)
			}
		}
		rows.Close()
	}
	s.db.Exec(`DELETE FROM sessions WHERE user_id = ? AND token_hash != ?`, uid, keepHash)
	return gone
}

func (s *Store) passwordHash(uid int64) string {
	var h string
	s.db.QueryRow(`SELECT password_hash FROM users WHERE id = ?`, uid).Scan(&h)
	return h
}

func (s *Store) setPassword(uid int64, password string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`UPDATE users SET password_hash = ? WHERE id = ?`, string(hash), uid)
	return err
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

// attempt records one try for key and reports whether it is within max per window. The row id
// is returned so a successful try can be taken back (releaseAttempt). Callers serialize per
// limiter (limiter.mu), so the count and the insert cannot interleave.
func (s *Store) attempt(key string, max int, window time.Duration) (int64, bool, error) {
	now := time.Now()
	cutoff := now.Add(-window).Unix()
	if _, err := s.db.Exec(`DELETE FROM attempts WHERE at <= ?`, cutoff); err != nil {
		return 0, false, err
	}
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM attempts WHERE key = ? AND at > ?`, key, cutoff).Scan(&n); err != nil {
		return 0, false, err
	}
	if n >= max {
		return 0, false, nil
	}
	res, err := s.db.Exec(`INSERT INTO attempts (key, at) VALUES (?, ?)`, key, now.Unix())
	if err != nil {
		return 0, false, err
	}
	id, err := res.LastInsertId()
	return id, err == nil, err
}

func (s *Store) releaseAttempt(id int64) {
	s.db.Exec(`DELETE FROM attempts WHERE rowid = ?`, id)
}

type AuditEntry struct {
	ID       int64  `json:"id"`
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

// auditCategories map an Activity filter to action prefixes.
var auditCategories = map[string][]string{
	"signin":   {"login.", "logout", "reauth.", "2fa.", "account."},
	"console":  {"console."},
	"apps":     {"app.", "docker.", "catalog.", "images.", "proxy.", "backup."},
	"files":    {"files."},
	"firewall": {"firewall."},
	"system":   {"settings.", "box.", "notify."},
}

type auditQuery struct {
	Limit    int
	Cursor   int64 // id to continue after (0 = start)
	Asc      bool
	Category string
	User     string
	Search   string
}

// queryAudit returns one page of the log, newest (or oldest) first, plus the total that matches.
func (s *Store) queryAudit(q auditQuery) ([]AuditEntry, int, error) {
	where, args := []string{"1=1"}, []any{}
	if prefixes, ok := auditCategories[q.Category]; ok {
		var or []string
		for _, p := range prefixes {
			or = append(or, "action LIKE ? ESCAPE '\\'")
			args = append(args, likeEscape(p)+"%")
		}
		where = append(where, "("+strings.Join(or, " OR ")+")")
	}
	if q.User != "" {
		where = append(where, "username = ?")
		args = append(args, q.User)
	}
	if q.Search != "" {
		where = append(where, "(action LIKE ? ESCAPE '\\' OR detail LIKE ? ESCAPE '\\' OR ip LIKE ? ESCAPE '\\' OR username LIKE ? ESCAPE '\\')")
		like := "%" + likeEscape(q.Search) + "%"
		args = append(args, like, like, like, like)
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM audit WHERE `+strings.Join(where, " AND "), args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order, cmp := "DESC", "<"
	if q.Asc {
		order, cmp = "ASC", ">"
	}
	if q.Cursor > 0 {
		where = append(where, "id "+cmp+" ?")
		args = append(args, q.Cursor)
	}
	args = append(args, q.Limit)
	rows, err := s.db.Query(`SELECT id, at, username, ip, action, detail FROM audit WHERE `+strings.Join(where, " AND ")+` ORDER BY id `+order+` LIMIT ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.At, &e.Username, &e.IP, &e.Action, &e.Detail); err != nil {
			return nil, 0, err
		}
		out = append(out, e)
	}
	return out, total, rows.Err()
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func (s *Store) auditUsers() []string {
	out := []string{}
	rows, err := s.db.Query(`SELECT DISTINCT username FROM audit WHERE username != '' ORDER BY username LIMIT 100`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var u string
		if rows.Scan(&u) == nil {
			out = append(out, u)
		}
	}
	return out
}

func (s *Store) setting(key string) string {
	var v string
	s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	return v
}

func (s *Store) setSetting(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// ---- jobs ----

func (s *Store) saveJob(row []any) error {
	_, err := s.db.Exec(`INSERT INTO jobs (id, kind, app, status, started, finished, error, lines, result)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET status = excluded.status, finished = excluded.finished,
			error = excluded.error, lines = excluded.lines, result = excluded.result`, row...)
	return err
}

func (s *Store) loadJob(id string) *job {
	j := &job{}
	var lines, res string
	err := s.db.QueryRow(`SELECT id, kind, app, status, started, finished, error, lines, result FROM jobs WHERE id = ?`, id).
		Scan(&j.ID, &j.Kind, &j.App, &j.Status, &j.Started, &j.Finished, &j.Error, &lines, &res)
	if err != nil {
		return nil
	}
	json.Unmarshal([]byte(lines), &j.Lines)
	json.Unmarshal([]byte(res), &j.Result)
	if j.Lines == nil {
		j.Lines = []string{}
	}
	return j
}

// interruptJobs ends the jobs a previous run left running and drops old ones.
func (s *Store) interruptJobs() (int64, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(`UPDATE jobs SET status = 'interrupted', error = ?, finished = ? WHERE status = 'running'`, interruptedMsg, now)
	if err != nil {
		return 0, err
	}
	s.db.Exec(`DELETE FROM jobs WHERE finished > 0 AND finished < ?`, now-7*24*3600)
	return res.RowsAffected()
}
