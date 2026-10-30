package box

// Guards for app installs: what a compose file may mount from the host, where the Runtipi and
// CasaOS "root" variables point, and how password questions are answered.

import (
	"cmp"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/NexoVirt/nexovirt-box/internal/catalog"
)

// sharedDir holds media and downloads that apps share (Runtipi's ROOT_FOLDER_HOST/media,
// CasaOS's SHARED_DIR). Box keeps none of its own state there.
func sharedDir(base string) string { return filepath.Join(base, "shared") }

type mountPolicy struct {
	base        string   // Box's state dir (/var/lib/nexovirt-box)
	allow       []string // folders inside base the app may use: its own dir, its data dir, the shared dir
	appDir      string   // the app's own folder: never mounted itself, nor its compose, .env and settings
	allowSocket bool     // the catalog marks the app as needing Docker and the admin agreed
	custom      bool     // an own app: raw compose and privilege checks too (customapp.go)
	roots       []string // own apps: the file roots, binds inside them need no confirmation
	confirmed   []string // own apps: risk findings the admin confirmed (riskFinding keys)
	reviewed    bool     // own apps saved with the per-item review; older ones keep legacyOK
	protectPort int      // Box sign-in in front: this port on 127.0.0.1 only; -1 = protected without a port (refused)
}

// Folders that are never mounted, nor any folder that contains them.
var (
	boxDirs     = []string{"/etc/nexovirt-box", "/run/nexovirt-box"}
	noMountDirs = []string{"/etc", "/root", "/proc", "/sys", "/boot"}
	// Also never mounted, nor anything below them.
	secretPaths = []string{"/root", "/etc/shadow", "/etc/gshadow", "/etc/sudoers", "/etc/sudoers.d", "/etc/ssh"}
	sockets     = []string{"/run/docker.sock", "/var/run/docker.sock"}
)

// checkMounts reads `docker compose config --format json` and refuses every host path an app
// must not see. Paths are resolved through symlinks first (a container can plant one in its
// own data folder).
func checkMounts(configJSON []byte, p mountPolicy) error {
	var cfg struct {
		Services map[string]struct {
			Volumes []struct {
				Type   string `json:"type"`
				Source string `json:"source"`
			} `json:"volumes"`
		} `json:"services"`
		Volumes map[string]struct {
			DriverOpts map[string]string `json:"driver_opts"`
		} `json:"volumes"`
	}
	if err := json.Unmarshal(configJSON, &cfg); err != nil {
		return uerr("The app definition could not be read.")
	}
	var sources []string
	for _, s := range cfg.Services {
		for _, v := range s.Volumes {
			switch v.Type {
			case "bind":
				sources = append(sources, v.Source)
			case "volume":
				if o := cfg.Volumes[v.Source].DriverOpts; o["device"] != "" && (strings.Contains(o["o"], "bind") || o["type"] == "none") {
					sources = append(sources, o["device"])
				}
			}
		}
	}
	for _, src := range sources {
		if err := p.check(src); err != nil {
			return err
		}
	}
	return nil
}

func (p mountPolicy) check(src string) error {
	if !filepath.IsAbs(src) {
		return uerr("This app uses a folder Box cannot check (%s). It was not installed.", src)
	}
	path := resolvePath(src)
	refuse := func(what string) error {
		return uerr("This app would get access to %s (%s). Box refused it to keep the server safe.", what, src)
	}
	if path == "/" {
		return refuse("the whole server")
	}
	for _, s := range sockets {
		if path == resolvePath(s) || filepath.Base(path) == "docker.sock" {
			if p.allowSocket {
				return nil
			}
			return refuse("Docker, which means full control of the server")
		}
	}
	// The app folder holds compose.yaml, .env and app.json, which decide what Box starts as root:
	// a container that can write them gets the server. Its subfolders (data) are fine.
	if p.appDir != "" {
		if d := resolvePath(p.appDir); path == d || (filepath.Dir(path) == d && appControlFile(filepath.Base(path))) {
			return refuse("NexoVirt Box's own files")
		}
	}
	for _, d := range append([]string{p.base}, boxDirs...) {
		d = resolvePath(d)
		if path == d || within(d, path) {
			return refuse("NexoVirt Box's own files")
		}
		if within(path, d) && !p.allowed(path) {
			return refuse("NexoVirt Box's own files")
		}
	}
	for _, d := range noMountDirs {
		d = resolvePath(d)
		if path == d || within(d, path) {
			return refuse("a system folder")
		}
	}
	for _, d := range secretPaths {
		d = resolvePath(d)
		if path == d || within(path, d) {
			return refuse("system passwords or keys")
		}
	}
	return nil
}

// appControlFile: the files Box keeps in an app's folder (everything but its subfolders).
func appControlFile(name string) bool {
	switch name {
	case "compose.yaml", ".env", "app.json", limitsFile, bindFile, cloneFile, installMarker:
		return true
	}
	return strings.HasPrefix(name, ".") || strings.HasSuffix(name, ".yaml") || strings.HasSuffix(name, ".json")
}

func (p mountPolicy) allowed(path string) bool {
	for _, a := range p.allow {
		a = resolvePath(a)
		if path == a || within(path, a) {
			return true
		}
	}
	return false
}

// within reports whether path lies below dir.
func within(path, dir string) bool {
	return strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/")
}

// resolvePath follows symlinks of the longest existing prefix (Docker creates the rest).
func resolvePath(p string) string {
	p = filepath.Clean(p)
	rest := ""
	for cur := p; ; cur = filepath.Dir(cur) {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, rest)
		}
		if cur == "/" || cur == "." {
			return p
		}
		rest = filepath.Join(filepath.Base(cur), rest)
	}
}

// ---- .env ----

func parseEnv(b []byte) map[string]string {
	out := map[string]string{}
	for _, l := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(l, "=")
		if !ok || strings.HasPrefix(l, "#") {
			continue
		}
		out[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), "'")
	}
	return out
}

// fixLegacyEnv repoints the Box variables of apps installed before ROOT_FOLDER_HOST moved off
// the state dir. Only Box's own keys change; the compose file stays untouched.
func fixLegacyEnv(dir, base string) error {
	path := filepath.Join(dir, ".env")
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	want := map[string]string{"ROOT_FOLDER_HOST": sharedDir(base), "SHARED_DIR": sharedDir(base)}
	lines := strings.Split(string(b), "\n")
	changed := false
	for i, l := range lines {
		k, v, ok := strings.Cut(l, "=")
		if w, mine := want[k]; ok && mine && strings.Trim(v, "'") != w {
			lines[i] = k + "='" + w + "'"
			changed = true
		}
	}
	if !changed {
		return nil
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// ---- password questions ----

var sudoKeyRe = regexp.MustCompile(`(?i)SUDO`)

type presentedQuestion struct {
	catalog.Question
	Locked bool `json:"locked,omitempty"` // always generated, the admin cannot set it
}

// presentQuestions is how Box asks: every password is generated unless the admin sets one,
// and a sudo password for inside a container is always generated (it is never a login).
func presentQuestions(qs []catalog.Question) []presentedQuestion {
	out := make([]presentedQuestion, 0, len(qs))
	for _, q := range qs {
		pq := presentedQuestion{Question: q}
		if q.Type == "password" {
			pq.Generate, pq.Required = true, false
			if sudoKeyRe.MatchString(q.Key) {
				pq.Locked = true
				pq.Label = "Password for sudo inside the app"
				pq.Hint = "Box generates it. You only need it to run sudo in the app's own terminal."
			}
		}
		out = append(out, pq)
	}
	return out
}

// generateValue is a random secret for a question left empty: format base64 is Min random bytes
// (default 32), base64 encoded; anything else a password that fits Min and Max.
func generateValue(q catalog.Question) string {
	if q.Format == "base64" {
		b := make([]byte, cmp.Or(q.Min, 32))
		rand.Read(b)
		return base64.StdEncoding.EncodeToString(b)
	}
	return randomSecret(generatedLen(q))
}

func generatedLen(q catalog.Question) int {
	n := max(q.Min, 32)
	if q.Max > 0 && n > q.Max {
		n = q.Max
	}
	return n
}
