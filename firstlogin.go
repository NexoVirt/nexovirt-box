package main

// First-login details: many apps create their own admin account on first start and print the
// initial password only in their logs. The catalog can say where it is (first_login); without
// that, Box scans the first log lines for common patterns and offers what it finds.

import (
	"bufio"
	"context"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"time"

	"github.com/nexovirt/box/internal/catalog"
)

// firstLogLines is how many lines from the start of an app's logs are scanned.
const firstLogLines = 1000

type appCred struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
	Locked bool   `json:"locked,omitempty"`
	Source string `json:"source,omitempty"` // "log": read from the app's logs, may have changed since
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

// scanLog returns the first capture of re in lines.
func scanLog(lines []string, re *regexp.Regexp) string {
	for _, l := range lines {
		if m := re.FindStringSubmatch(l); m != nil && strings.TrimSpace(m[1]) != "" {
			return strings.TrimSpace(m[1])
		}
	}
	return ""
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

// firstLoginCreds adds the catalog's first-login details (or what the logs show) to have.
// logs is only called when needed, it runs docker.
func firstLoginCreds(app *catalog.App, env map[string]string, have []appCred, logs func() []string) []appCred {
	known := map[string]bool{}
	for _, c := range have {
		known[c.Value] = true
	}
	out := have
	add := func(c appCred) {
		if c.Value != "" && !known[c.Value] {
			known[c.Value] = true
			out = append(out, c)
		}
	}
	fl := app.FirstLogin
	if fl == nil {
		for _, v := range genericLogSecrets(logs()) {
			add(appCred{Label: "Password found in the logs", Value: v, Secret: true, Source: "log"})
		}
		return out
	}
	if fl.Username != "" {
		add(appCred{Label: "User name", Value: fl.Username})
	}
	if fl.PasswordFromEnv != "" {
		add(appCred{Label: "Password", Value: env[fl.PasswordFromEnv], Secret: true})
	}
	if re, err := fl.LogRegexp(); err == nil && re != nil {
		add(appCred{Label: "Initial password", Value: scanLog(logs(), re), Secret: true, Source: "log"})
	}
	return out
}
