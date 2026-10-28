package main

// Notifications: channels (email, Discord, ntfy, generic webhook), one rule per event (on/off
// and which channels), deduplication per event key, a global rate limit and a history.
// Channel secrets (SMTP password, webhook URLs, tokens) are sealed with AES-256-GCM under a
// random key in <state>/notify.key (0600) and never sent back to the browser.

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	evAppDown        = "app_down"
	evAppRecovered   = "app_recovered"
	evDiskHigh       = "disk_high"
	evUpdate         = "update_available"
	evBackupFailed   = "backup_failed"
	evBackupOK       = "backup_ok"
	evLoginNewIP     = "login_new_ip"
	evLoginFailed    = "login_failed_burst"
	evFirewallRolled = "firewall_rollback"
)

type notifyEvent struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Hint  string `json:"hint"`
	On    bool   `json:"default_on"`
}

var notifyEvents = []notifyEvent{
	{evAppDown, "An app went down", "It stopped although nobody stopped it.", true},
	{evAppRecovered, "An app is running again", "After it went down.", true},
	{evDiskHigh, "A disk is getting full", "Above the threshold below.", true},
	{evUpdate, "Updates are available", "For apps or for NexoVirt Box.", true},
	{evBackupFailed, "A backup failed", "Scheduled or started by hand.", true},
	{evBackupOK, "A backup succeeded", "One message per backup.", false},
	{evLoginNewIP, "Sign-in from a new address", "The first sign-in from an IP address Box has not seen.", true},
	{evLoginFailed, "Many failed sign-ins", "5 or more wrong passwords within 10 minutes.", true},
	{evFirewallRolled, "Firewall change rolled back", "A change was not confirmed in time.", true},
}

func eventKnown(k string) bool {
	for _, e := range notifyEvents {
		if e.Key == k {
			return true
		}
	}
	return false
}

type notifyChannel struct {
	ID      int64             `json:"id"`
	Kind    string            `json:"kind"`
	Name    string            `json:"name"`
	Enabled bool              `json:"enabled"`
	Config  map[string]string `json:"config"`
	Has     []string          `json:"has,omitempty"` // secrets that are set (values never leave the server)
}

type notifyRule struct {
	Enabled  bool    `json:"enabled"`
	All      bool    `json:"all"`      // every enabled channel
	Channels []int64 `json:"channels"` // used when All is false
}

// Fields per channel kind; secret fields are sealed in the database.
// dest are the fields that decide where a message (and so a stored secret) goes: when one of
// them changes, kept secrets are dropped and must be entered again.
var channelFields = map[string]struct{ fields, secrets, dest []string }{
	"email":   {[]string{"host", "port", "security", "username", "password", "from", "to"}, []string{"password"}, []string{"host", "port", "security", "username"}},
	"discord": {[]string{"url"}, []string{"url"}, nil},
	"ntfy":    {[]string{"server", "topic", "token"}, []string{"token"}, []string{"server", "topic"}},
	"webhook": {[]string{"url", "token"}, []string{"url", "token"}, []string{"url"}},
}

const (
	notifyBurst  = 20               // messages per window
	notifyWindow = 10 * time.Minute // for the rate limit
	notifyDedup  = 30 * time.Minute // same event key again only after this
)

type notifier struct {
	store  *Store
	aead   cipher.AEAD
	mu     sync.Mutex
	sent   []time.Time          // for the rate limit
	last   map[string]time.Time // event key -> last sent
	wg     sync.WaitGroup
	client *http.Client
}

func newNotifier(store *Store, state string) (*notifier, error) {
	key, err := loadOrCreateKey(filepath.Join(state, "notify.key"))
	if err != nil {
		return nil, err
	}
	block, _ := aes.NewCipher(key)
	aead, _ := cipher.NewGCM(block)
	_, err = store.db.Exec(`
		CREATE TABLE IF NOT EXISTS notify_channels (id INTEGER PRIMARY KEY AUTOINCREMENT, kind TEXT NOT NULL, name TEXT NOT NULL,
			enabled INTEGER NOT NULL DEFAULT 1, config TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS notify_rules (event TEXT PRIMARY KEY, rule TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS notify_log (id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, event TEXT NOT NULL,
			title TEXT NOT NULL, message TEXT NOT NULL, result TEXT NOT NULL);
		CREATE TABLE IF NOT EXISTS known_ips (ip TEXT PRIMARY KEY, first_seen INTEGER NOT NULL);`)
	if err != nil {
		return nil, err
	}
	return &notifier{store: store, aead: aead, last: map[string]time.Time{},
		client: &http.Client{Timeout: 15 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

func loadOrCreateKey(path string) ([]byte, error) {
	if b, err := os.ReadFile(path); err == nil && len(b) == 32 {
		return b, nil
	}
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, err
	}
	return k, os.WriteFile(path, k, 0o600)
}

func (n *notifier) seal(s string) string {
	if s == "" {
		return ""
	}
	nonce := make([]byte, n.aead.NonceSize())
	rand.Read(nonce)
	return "enc:" + base64.StdEncoding.EncodeToString(n.aead.Seal(nonce, nonce, []byte(s), nil))
}

func (n *notifier) open(s string) string {
	b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, "enc:"))
	if err != nil || len(b) < n.aead.NonceSize() {
		return ""
	}
	pt, err := n.aead.Open(nil, b[:n.aead.NonceSize()], b[n.aead.NonceSize():], nil)
	if err != nil {
		return ""
	}
	return string(pt)
}

// ---- validation ----

var (
	ntfyTopicRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	emailAddrRe = regexp.MustCompile(`^[^\s@<>,;"]+@[^\s@<>,;"]+\.[^\s@<>,;"]+$`)
	smtpHostRe  = regexp.MustCompile(`^[A-Za-z0-9.-]{1,253}$`)
)

// checkNotifyURL allows https everywhere and plain http only to this machine or a private
// network (a local receiver). No credentials in the URL.
func checkNotifyURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") || len(raw) > 1000 {
		return uerr("Enter a full https:// address.")
	}
	if u.Scheme == "http" && !privateHost(u.Hostname()) {
		return uerr("Plain http:// is only allowed to this server or a private network. Use https://.")
	}
	return nil
}

func (n *notifier) validate(c notifyChannel) error {
	if c.Name = strings.TrimSpace(c.Name); c.Name == "" || len(c.Name) > 60 || controlChar.MatchString(c.Name) {
		return uerr("Give the channel a name of up to 60 characters.")
	}
	cfg := c.Config
	for k, v := range cfg {
		if controlChar.MatchString(v) || len(v) > 1000 {
			return uerr("The field %s has characters Box cannot use.", k)
		}
	}
	switch c.Kind {
	case "email":
		if !smtpHostRe.MatchString(cfg["host"]) {
			return uerr("Enter the mail server, for example smtp.example.com.")
		}
		if p, err := strconv.Atoi(cfg["port"]); err != nil || p < 1 || p > 65535 {
			return uerr("Enter the mail server port (587, 465 or 25).")
		}
		if cfg["security"] != "starttls" && cfg["security"] != "tls" && cfg["security"] != "none" {
			return uerr("Pick how Box connects to the mail server.")
		}
		if cfg["security"] == "none" && !privateHost(cfg["host"]) {
			return uerr("Unencrypted mail is only allowed to this server or a private network.")
		}
		if cfg["username"] != "" && cfg["password"] == "" {
			return uerr("Enter the password for the mail server user.")
		}
		if !emailAddrRe.MatchString(cfg["from"]) {
			return uerr("Enter the sender address.")
		}
		for _, to := range strings.Split(cfg["to"], ",") {
			if !emailAddrRe.MatchString(strings.TrimSpace(to)) {
				return uerr("Enter one or more recipient addresses, separated by commas.")
			}
		}
	case "discord":
		u, err := url.Parse(cfg["url"])
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return uerr("Paste the Discord webhook URL (https://discord.com/api/webhooks/...).")
		}
	case "ntfy":
		if cfg["server"] == "" {
			cfg["server"] = "https://ntfy.sh"
		}
		if err := checkNotifyURL(cfg["server"]); err != nil {
			return err
		}
		if !ntfyTopicRe.MatchString(cfg["topic"]) {
			return uerr("The topic may use letters, digits, dashes and underscores (up to 64).")
		}
	case "webhook":
		if err := checkNotifyURL(cfg["url"]); err != nil {
			return err
		}
	default:
		return uerr("Pick a channel type.")
	}
	return nil
}

// ---- channels ----

func (n *notifier) saveChannel(c notifyChannel) (int64, error) {
	spec, ok := channelFields[c.Kind]
	if !ok {
		return 0, uerr("Pick a channel type.")
	}
	clean := map[string]string{}
	for _, f := range spec.fields {
		clean[f] = strings.TrimSpace(c.Config[f])
	}
	if c.ID != 0 { // an empty secret in the form keeps the stored one
		old, err := n.channel(c.ID)
		if err != nil {
			return 0, err
		}
		same := old.Kind == c.Kind
		for _, d := range spec.dest {
			// An empty secret URL keeps the stored one, so it is the same destination.
			if clean[d] != "" && clean[d] != old.Config[d] {
				same = false
			}
		}
		if same {
			for _, s := range spec.secrets {
				if clean[s] == "" {
					clean[s] = old.Config[s]
				}
			}
		}
	}
	c.Config = clean
	if err := n.validate(c); err != nil {
		return 0, err
	}
	stored := map[string]string{}
	for k, v := range clean {
		stored[k] = v
	}
	for _, s := range spec.secrets {
		stored[s] = n.seal(clean[s])
	}
	b, _ := json.Marshal(stored)
	en := 0
	if c.Enabled {
		en = 1
	}
	if c.ID == 0 {
		res, err := n.store.db.Exec(`INSERT INTO notify_channels (kind, name, enabled, config) VALUES (?, ?, ?, ?)`, c.Kind, strings.TrimSpace(c.Name), en, string(b))
		if err != nil {
			return 0, err
		}
		return res.LastInsertId()
	}
	_, err := n.store.db.Exec(`UPDATE notify_channels SET kind = ?, name = ?, enabled = ?, config = ? WHERE id = ?`, c.Kind, strings.TrimSpace(c.Name), en, string(b), c.ID)
	return c.ID, err
}

func (n *notifier) channels() []notifyChannel {
	rows, err := n.store.db.Query(`SELECT id, kind, name, enabled, config FROM notify_channels ORDER BY id`)
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []notifyChannel
	for rows.Next() {
		var c notifyChannel
		var cfg string
		if rows.Scan(&c.ID, &c.Kind, &c.Name, &c.Enabled, &cfg) != nil {
			continue
		}
		json.Unmarshal([]byte(cfg), &c.Config)
		for _, s := range channelFields[c.Kind].secrets {
			c.Config[s] = n.open(c.Config[s])
		}
		out = append(out, c)
	}
	return out
}

func (n *notifier) channel(id int64) (notifyChannel, error) {
	for _, c := range n.channels() {
		if c.ID == id {
			return c, nil
		}
	}
	return notifyChannel{}, uerr("This channel does not exist any more.")
}

// channelsPublic is what the browser sees: secrets removed, only whether they are set.
func (n *notifier) channelsPublic() []notifyChannel {
	out := []notifyChannel{}
	for _, c := range n.channels() {
		for _, s := range channelFields[c.Kind].secrets {
			if c.Config[s] != "" {
				c.Has = append(c.Has, s)
			}
			c.Config[s] = ""
		}
		out = append(out, c)
	}
	return out
}

func (n *notifier) deleteChannel(id int64) error {
	_, err := n.store.db.Exec(`DELETE FROM notify_channels WHERE id = ?`, id)
	return err
}

// ---- rules ----

func (n *notifier) rules() map[string]notifyRule {
	out := map[string]notifyRule{}
	for _, e := range notifyEvents {
		out[e.Key] = notifyRule{Enabled: e.On, All: true, Channels: []int64{}}
	}
	rows, err := n.store.db.Query(`SELECT event, rule FROM notify_rules`)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var ev, raw string
		var r notifyRule
		if rows.Scan(&ev, &raw) == nil && json.Unmarshal([]byte(raw), &r) == nil && eventKnown(ev) {
			if r.Channels == nil {
				r.Channels = []int64{}
			}
			out[ev] = r
		}
	}
	return out
}

func (n *notifier) setRules(rs map[string]notifyRule) error {
	for ev, r := range rs {
		if !eventKnown(ev) {
			return uerr("Unknown event: %s", ev)
		}
		if len(r.Channels) > 50 {
			return uerr("Too many channels.")
		}
		b, _ := json.Marshal(r)
		if _, err := n.store.db.Exec(`INSERT INTO notify_rules (event, rule) VALUES (?, ?) ON CONFLICT(event) DO UPDATE SET rule = excluded.rule`, ev, string(b)); err != nil {
			return err
		}
	}
	return nil
}

// ---- sending ----

// allowRate takes one message from the global budget.
func (n *notifier) allowRate(now time.Time) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	kept := n.sent[:0]
	for _, t := range n.sent {
		if now.Sub(t) < notifyWindow {
			kept = append(kept, t)
		}
	}
	n.sent = kept
	if len(n.sent) >= notifyBurst {
		return false
	}
	n.sent = append(n.sent, now)
	return true
}

// send delivers an event to the channels its rule picks, in the background. key identifies the
// thing it is about (app id, disk), so the same alert is not repeated within notifyDedup.
func (n *notifier) send(event, key, title, msg string) {
	if n == nil {
		return
	}
	rule := n.rules()[event]
	if !rule.Enabled {
		return
	}
	now := time.Now()
	dk := event + "|" + key
	n.mu.Lock()
	if t, ok := n.last[dk]; ok && now.Sub(t) < notifyDedup {
		n.mu.Unlock()
		return
	}
	n.last[dk] = now
	n.mu.Unlock()
	var targets []notifyChannel
	picked := map[int64]bool{}
	for _, id := range rule.Channels {
		picked[id] = true
	}
	for _, c := range n.channels() {
		if c.Enabled && (rule.All || picked[c.ID]) {
			targets = append(targets, c)
		}
	}
	if len(targets) == 0 {
		return
	}
	if !n.allowRate(now) {
		n.log(event, title, msg, "Not sent: too many messages in the last 10 minutes.")
		return
	}
	n.wg.Add(1)
	go func() {
		defer n.wg.Done()
		var res []string
		for _, c := range targets {
			if err := n.deliver(c, event, title, msg); err != nil {
				log.Printf("notify %s via %s: %v", event, c.Name, err)
				res = append(res, c.Name+": "+friendly(err))
			} else {
				res = append(res, c.Name+": sent")
			}
		}
		n.log(event, title, msg, strings.Join(res, "; "))
	}()
}

func (n *notifier) wait() { n.wg.Wait() }

func (n *notifier) log(event, title, msg, result string) {
	n.store.db.Exec(`INSERT INTO notify_log (at, event, title, message, result) VALUES (?, ?, ?, ?, ?)`, time.Now().Unix(), event, title, msg, result)
	n.store.db.Exec(`DELETE FROM notify_log WHERE id <= (SELECT MAX(id) - 500 FROM notify_log)`)
}

type notifyLogEntry struct {
	At      int64  `json:"at"`
	Event   string `json:"event"`
	Title   string `json:"title"`
	Message string `json:"message"`
	Result  string `json:"result"`
}

func (n *notifier) history(limit int) []notifyLogEntry {
	out := []notifyLogEntry{}
	rows, err := n.store.db.Query(`SELECT at, event, title, message, result FROM notify_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return out
	}
	defer rows.Close()
	for rows.Next() {
		var e notifyLogEntry
		if rows.Scan(&e.At, &e.Event, &e.Title, &e.Message, &e.Result) == nil {
			out = append(out, e)
		}
	}
	return out
}

func (n *notifier) deliver(c notifyChannel, event, title, msg string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	host, _ := os.Hostname()
	cfg := c.Config
	switch c.Kind {
	case "email":
		return sendMail(cfg, strings.NewReplacer("\r", " ", "\n", " ").Replace("[NexoVirt Box "+host+"] "+title), msg)
	case "discord":
		text := "**" + title + "**"
		if msg != "" {
			text += "\n" + msg
		}
		b, _ := json.Marshal(map[string]any{"content": trimTo(text, 1900), "username": "NexoVirt Box", "allowed_mentions": map[string]any{"parse": []string{}}})
		return n.post(ctx, cfg["url"], "application/json", b, nil)
	case "ntfy":
		hdr := map[string]string{"Title": title, "Tags": ntfyTag(event)}
		if cfg["token"] != "" {
			hdr["Authorization"] = "Bearer " + cfg["token"]
		}
		body := msg
		if body == "" {
			body = title
		}
		return n.post(ctx, strings.TrimRight(cfg["server"], "/")+"/"+cfg["topic"], "text/plain; charset=utf-8", []byte(body), hdr)
	case "webhook":
		b, _ := json.Marshal(map[string]any{"event": event, "title": title, "message": msg, "host": host, "at": time.Now().Unix()})
		var hdr map[string]string
		if cfg["token"] != "" {
			hdr = map[string]string{"Authorization": "Bearer " + cfg["token"]}
		}
		return n.post(ctx, cfg["url"], "application/json", b, hdr)
	}
	return errors.New("unknown channel kind")
}

func ntfyTag(event string) string {
	switch event {
	case evAppRecovered, evBackupOK:
		return "white_check_mark"
	case evUpdate:
		return "arrow_up"
	}
	return "warning"
}

func trimTo(s string, n int) string {
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func (n *notifier) post(ctx context.Context, u, ctype string, body []byte, hdr map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(body))
	if err != nil {
		return uerr("The address is not valid.")
	}
	req.Header.Set("Content-Type", ctype)
	req.Header.Set("User-Agent", "NexoVirt-Box/"+version)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	res, err := n.client.Do(req)
	if err != nil {
		return uerr("The address could not be reached.")
	}
	res.Body.Close()
	if res.StatusCode >= 300 {
		return uerr("The receiver answered with status %d.", res.StatusCode)
	}
	return nil
}

func sendMail(cfg map[string]string, subject, body string) error {
	addr := net.JoinHostPort(cfg["host"], cfg["port"])
	tcfg := &tls.Config{ServerName: cfg["host"], MinVersion: tls.VersionTLS12}
	var conn net.Conn
	var err error
	d := &net.Dialer{Timeout: 15 * time.Second}
	if cfg["security"] == "tls" {
		conn, err = tls.DialWithDialer(d, "tcp", addr, tcfg)
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return uerr("The mail server %s could not be reached.", addr)
	}
	conn.SetDeadline(time.Now().Add(30 * time.Second))
	c, err := smtp.NewClient(conn, cfg["host"])
	if err != nil {
		conn.Close()
		return uerr("The mail server did not answer like a mail server.")
	}
	defer c.Close()
	if cfg["security"] == "starttls" {
		if err := c.StartTLS(tcfg); err != nil {
			return uerr("The mail server does not support STARTTLS with a valid certificate.")
		}
	}
	if cfg["username"] != "" {
		if err := c.Auth(smtp.PlainAuth("", cfg["username"], cfg["password"], cfg["host"])); err != nil {
			return uerr("The mail server refused the username or password.")
		}
	}
	if err := c.Mail(cfg["from"]); err != nil {
		return uerr("The mail server refused the sender address.")
	}
	to := strings.Split(cfg["to"], ",")
	for i := range to {
		to[i] = strings.TrimSpace(to[i])
		if err := c.Rcpt(to[i]); err != nil {
			return uerr("The mail server refused the recipient %s.", to[i])
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	fmt.Fprintf(w, "From: %s\r\nTo: %s\r\nSubject: %s\r\nDate: %s\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n%s\r\n",
		cfg["from"], strings.Join(to, ", "), subject, time.Now().Format(time.RFC1123Z), strings.ReplaceAll(body, "\n", "\r\n"))
	if err := w.Close(); err != nil {
		return uerr("The mail server did not accept the message.")
	}
	return c.Quit()
}
