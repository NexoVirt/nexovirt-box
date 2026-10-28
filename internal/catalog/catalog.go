// Package catalog holds the signed app catalog format shared by Box and the importer.
package catalog

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"strings"
)

type Question struct {
	Key         string   `json:"key"`
	Label       string   `json:"label"`
	Type        string   `json:"type"` // text password number email domain boolean select port
	Hint        string   `json:"hint,omitempty"`
	Placeholder string   `json:"placeholder,omitempty"`
	Default     string   `json:"default,omitempty"`
	Required    bool     `json:"required,omitempty"`
	Min         int      `json:"min,omitempty"`
	Max         int      `json:"max,omitempty"`
	Options     []string `json:"options,omitempty"`
	Generate    bool     `json:"generate,omitempty"` // random secret when left empty
	Format      string   `json:"format,omitempty"`   // "base64": generate Min random bytes, base64 encoded
}

type Attribution struct {
	Store   string `json:"store"`
	URL     string `json:"url"`
	License string `json:"license"`
}

// Summary is what the index carries for the store list.
type Summary struct {
	ID            string      `json:"id"`
	Name          string      `json:"name"`
	Version       string      `json:"version"`
	Category      string      `json:"category"`
	Tagline       string      `json:"tagline"`
	Icon          string      `json:"icon,omitempty"`
	Architectures []string    `json:"architectures,omitempty"`
	DockerSocket  bool        `json:"docker_socket,omitempty"`
	Attribution   Attribution `json:"attribution"`
}

type App struct {
	Summary
	Description string      `json:"description"`
	Screenshots []string    `json:"screenshots,omitempty"`
	Website     string      `json:"website,omitempty"`
	Source      string      `json:"source,omitempty"`
	MainPort    int         `json:"main_port"`
	Scheme      string      `json:"scheme,omitempty"`
	Questions   []Question  `json:"questions"`
	Compose     string      `json:"compose"`
	FirstLogin  *FirstLogin `json:"first_login,omitempty"`
	Notes       string      `json:"notes,omitempty"` // "Getting started" text, plain text
}

// FirstLogin says how to sign in to an app the first time, when the app makes its own admin
// account. PasswordFromLog is a regular expression with one capture group, matched against
// the first log lines of the app; PasswordFromEnv names a variable of the app's .env.
type FirstLogin struct {
	Username        string `json:"username,omitempty"`
	PasswordFromLog string `json:"password_from_log_regex,omitempty"`
	PasswordFromEnv string `json:"password_from_env,omitempty"`
	Note            string `json:"note,omitempty"`
}

// LogRegexp compiles PasswordFromLog; it must have exactly one capture group.
func (f *FirstLogin) LogRegexp() (*regexp.Regexp, error) {
	if f == nil || f.PasswordFromLog == "" {
		return nil, nil
	}
	re, err := regexp.Compile(f.PasswordFromLog)
	if err != nil {
		return nil, err
	}
	if re.NumSubexp() != 1 {
		return nil, fmt.Errorf("first_login: the log pattern needs exactly one group")
	}
	return re, nil
}

type IndexEntry struct {
	Summary
	File   string `json:"file"`
	SHA256 string `json:"sha256"`
}

type Index struct {
	Version     int          `json:"version"`
	GeneratedAt int64        `json:"generated_at"`
	Apps        []IndexEntry `json:"apps"`
}

var IDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// Catalog is a verified catalog read from an fs.FS holding index.json, index.sig and apps/.
type Catalog struct {
	Index Index
	fsys  fs.FS
	byID  map[string]IndexEntry
}

var ErrSignature = errors.New("the catalog signature is not valid")

// Load verifies index.json against index.sig with pub and returns the catalog. App files are
// checked against their sha256 when read.
func Load(fsys fs.FS, pubs []ed25519.PublicKey) (*Catalog, error) {
	raw, err := fs.ReadFile(fsys, "index.json")
	if err != nil {
		return nil, err
	}
	sigB64, err := fs.ReadFile(fsys, "index.sig")
	if err != nil {
		return nil, err
	}
	sig, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(sigB64)))
	if err != nil || !verifyAny(pubs, raw, sig) {
		return nil, ErrSignature
	}
	var idx Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return nil, fmt.Errorf("catalog index: %w", err)
	}
	c := &Catalog{Index: idx, fsys: fsys, byID: map[string]IndexEntry{}}
	for _, e := range idx.Apps {
		if !IDRe.MatchString(e.ID) || path.Clean(e.File) != e.File || strings.HasPrefix(e.File, "..") || strings.HasPrefix(e.File, "/") {
			return nil, fmt.Errorf("catalog index: bad entry %q", e.ID)
		}
		c.byID[e.ID] = e
	}
	return c, nil
}

func verifyAny(pubs []ed25519.PublicKey, msg, sig []byte) bool {
	for _, p := range pubs {
		if ed25519.Verify(p, msg, sig) {
			return true
		}
	}
	return false
}

// App reads one app file and checks it against the signed index.
func (c *Catalog) App(id string) (*App, error) {
	e, ok := c.byID[id]
	if !ok {
		return nil, fs.ErrNotExist
	}
	raw, err := fs.ReadFile(c.fsys, e.File)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	if hex.EncodeToString(sum[:]) != e.SHA256 {
		return nil, ErrSignature
	}
	var a App
	if err := json.Unmarshal(raw, &a); err != nil {
		return nil, err
	}
	if a.ID != id {
		return nil, ErrSignature
	}
	return &a, nil
}

func (c *Catalog) Entry(id string) (IndexEntry, bool) {
	e, ok := c.byID[id]
	return e, ok
}

// Sign returns the base64 signature for index bytes.
func Sign(priv ed25519.PrivateKey, index []byte) string {
	return base64.StdEncoding.EncodeToString(ed25519.Sign(priv, index))
}

// FileHash is the sha256 written into the index.
func FileHash(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}
