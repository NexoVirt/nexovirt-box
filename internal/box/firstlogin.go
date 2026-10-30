package box

// First-login details: many apps create their own admin account on first start and print the
// initial password only in their logs. The catalog can say where it is (first_login); without
// that, Box scans the first log lines for common patterns and offers what it finds.

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/NexoVirt/nexovirt-box/internal/catalog"
)

// firstLogLines is how many lines from the start of an app's logs are scanned.
const firstLogLines = 1000

type appCred struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
	Locked bool   `json:"locked,omitempty"`
	// Source: "log" read from the app's logs, "file" read from a file the app wrote, "default" a
	// fixed default the app ships with. All three may have changed since.
	Source string `json:"source,omitempty"`
}

// firstLoginSources are the lazy readers firstLoginCreds needs: logs runs docker, file reads a
// file inside the app's data folder. Both are only called when the catalog asks for them.
type firstLoginSources struct {
	logs func() []string
	file func(rel string) ([]byte, error)
}

// maxCredFile is the largest first-login file Box reads.
const maxCredFile = 64 << 10

// readDataFile reads rel inside dataDir. os.Root refuses paths and symlinks that lead outside
// dataDir (the container writes this folder, so a link to /etc/shadow must not be followed);
// only small regular files are read.
func readDataFile(dataDir, rel string) ([]byte, error) {
	root, err := os.OpenRoot(dataDir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	f, err := root.Open(rel)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > maxCredFile {
		return nil, errors.New("not a small regular file")
	}
	return io.ReadAll(io.LimitReader(f, maxCredFile))
}

// fileSecret picks the password out of a first-login file: the regexp's group, else the whole
// file when it is one short line.
func fileSecret(b []byte, re *regexp.Regexp) string {
	if re != nil {
		if m := re.FindSubmatch(b); m != nil {
			return strings.TrimSpace(string(m[1]))
		}
		return ""
	}
	v := strings.TrimSpace(string(b))
	if strings.ContainsAny(v, "\r\n") || len(v) > 256 {
		return ""
	}
	return v
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]`)

// cleanLogLine drops the colour codes some apps print even with --no-color.
func cleanLogLine(l string) string { return ansiRe.ReplaceAllString(l, "") }

// readFirstLogLines returns up to n lines from the start of the app's logs (all containers).
func readFirstLogLines(dir, id string, n int) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := compose(dir, id, "logs", "--no-color", "--no-log-prefix")
	cmd := exec.CommandContext(ctx, c.Path, c.Args[1:]...)
	cmd.Dir = c.Dir
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return nil
	}
	go func() { pw.CloseWithError(cmd.Wait()) }()
	lines := []string{}
	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for sc.Scan() && len(lines) < n {
		lines = append(lines, cleanLogLine(sc.Text()))
	}
	cancel() // stops docker once we have enough
	io.Copy(io.Discard, pr)
	return lines
}

// scanLog returns the last capture of re in lines: apps that print a new password on every
// start (qBittorrent) mean the newest one.
func scanLog(lines []string, re *regexp.Regexp) string {
	v := ""
	for _, l := range lines {
		if m := re.FindStringSubmatch(l); m != nil && strings.TrimSpace(m[1]) != "" {
			v = strings.TrimSpace(m[1])
		}
	}
	return v
}

// genericSecretRes match the usual "initial password is: X" style lines. Each has one group.
var genericSecretRes = []*regexp.Regexp{
	regexp.MustCompile(`(?i)\b(?:initial|default|generated|temporary|random|one[- ]time)\s+(?:admin(?:istrator)?\s+|root\s+|user\s+)?(?:password|passwd|token)\b(?:\s+(?:is|was|set to|for\s+\S+))*\s*[:=]?\s*["'` + "`" + `]?([^\s"'` + "`" + `,;]{4,128})`),
	regexp.MustCompile(`(?i)\b(?:admin(?:istrator)?|root)\s+(?:password|passwd|token)\b(?:\s+(?:is|was|set to))*\s*[:=]\s*["'` + "`" + `]?([^\s"'` + "`" + `,;]{4,128})`),
	regexp.MustCompile(`(?i)\bdefault\s+credentials?\b\s*(?:are|is)?\s*[:=]?\s*\S+\s*(?:/|:)\s*([^\s"'` + "`" + `,;]{4,128})`),
}

// notSecret are words the patterns above catch in sentences like "the admin password is not set".
var notSecret = map[string]bool{"not": true, "has": true, "have": true, "been": true, "will": true, "must": true,
	"should": true, "changed": true, "required": true, "null": true, "none": true, "empty": true, "hidden": true,
	"unset": true, "missing": true, "found": true, "from": true, "with": true, "here": true, "below": true, "above": true,
	"true": true, "false": true, "enabled": true, "disabled": true, "length": true, "reset": true, "the": true}

// genericLogSecrets returns up to 3 distinct values that look like an initial password.
func genericLogSecrets(lines []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range lines {
		for _, re := range genericSecretRes {
			m := re.FindStringSubmatch(l)
			if m == nil {
				continue
			}
			v := strings.TrimRight(m[1], ".!)")
			if len(v) < 4 || notSecret[strings.ToLower(v)] || strings.Trim(v, "*x") == "" || seen[v] {
				continue
			}
			seen[v] = true
			out = append(out, v)
			if len(out) == 3 {
				return out
			}
			break
		}
	}
	return out
}

// firstLoginCreds puts the catalog's first-login details (or what the logs show) in front of
// have, the app's own answers; a value shows once. pending is true when the catalog names a
// password source that has nothing yet (the app has not started far enough).
func firstLoginCreds(app *catalog.App, env map[string]string, have []appCred, src firstLoginSources) (out []appCred, pending bool) {
	known := map[appCred]bool{} // by value and kind: user "admin" with password "admin" shows both
	add := func(c appCred) {
		k := appCred{Value: c.Value, Secret: c.Secret}
		if c.Value != "" && !known[k] {
			known[k] = true
			out = append(out, c)
		}
	}
	fl := app.FirstLogin
	if fl == nil {
		for _, c := range have {
			add(c)
		}
		for _, v := range genericLogSecrets(src.logs()) {
			add(appCred{Label: "Password found in the logs", Value: v, Secret: true, Source: "log"})
		}
		return out, false
	}
	if fl.Username != "" {
		add(appCred{Label: "User name", Value: fl.Username})
	}
	found := false
	pw := func(c appCred) {
		if c.Value != "" {
			found = true
		}
		add(c)
	}
	if fl.PasswordFromEnv != "" {
		pw(appCred{Label: "Password", Value: env[fl.PasswordFromEnv], Secret: true})
	}
	if rel, err := fl.FilePath(); err == nil && rel != "" && src.file != nil {
		if b, err := src.file(rel); err == nil {
			re, _ := fl.FileRegexp()
			pw(appCred{Label: "Initial password", Value: fileSecret(b, re), Secret: true, Source: "file"})
		}
	}
	if re, err := fl.LogRegexp(); err == nil && re != nil {
		pw(appCred{Label: "Initial password", Value: scanLog(src.logs(), re), Secret: true, Source: "log"})
	}
	if fl.Password != "" {
		pw(appCred{Label: "Default password", Value: fl.Password, Secret: true, Source: "default"})
	}
	for _, c := range have {
		add(c)
	}
	asked := fl.PasswordFromEnv != "" || fl.PasswordFromFile != "" || fl.PasswordFromLog != ""
	return out, asked && !found
}
