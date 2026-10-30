package box

// Central log search: the logs of every app (Docker) and of Box itself (journald: the web
// service and the helper) in one list, newest last, with a level guessed from the text. The
// helper reads (logs_all); the web side filters by text and level. The window polls with since=.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	logsAllPerSource = 300
	logsAllMax       = 2000
)

type sourcedLine struct {
	App     string `json:"a"`
	Service string `json:"s"`
	Time    string `json:"t"`
	Msg     string `json:"m"`
	Level   string `json:"l"` // error, warn, info, debug
	at      time.Time
}

var (
	errLevelRe   = regexp.MustCompile(`(?i)\b(error|err|fatal|panic|critical|crit|exception|failed)\b|level=error|"level":"error"`)
	warnLevelRe  = regexp.MustCompile(`(?i)\b(warn|warning)\b|level=warn|"level":"warn`)
	debugLevelRe = regexp.MustCompile(`(?i)\b(debug|trace)\b|level=debug|"level":"debug"`)
)

func levelOf(msg string) string {
	switch {
	case errLevelRe.MatchString(msg):
		return "error"
	case warnLevelRe.MatchString(msg):
		return "warn"
	case debugLevelRe.MatchString(msg):
		return "debug"
	}
	return "info"
}

// parseJournal reads `journalctl -o json` lines.
func parseJournal(out []byte) []sourcedLine {
	var lines []sourcedLine
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		var e struct {
			TS   string `json:"__REALTIME_TIMESTAMP"`
			Unit string `json:"_SYSTEMD_UNIT"`
			Msg  any    `json:"MESSAGE"`
		}
		if json.Unmarshal(sc.Bytes(), &e) != nil {
			continue
		}
		us, err := strconv.ParseInt(e.TS, 10, 64)
		if err != nil {
			continue
		}
		msg := ""
		switch m := e.Msg.(type) {
		case string:
			msg = m
		case []any: // journald sends non-UTF-8 messages as byte arrays
			b := make([]byte, 0, len(m))
			for _, x := range m {
				if f, ok := x.(float64); ok {
					b = append(b, byte(f))
				}
			}
			msg = string(b)
		}
		at := time.UnixMicro(us)
		svc := strings.TrimSuffix(e.Unit, ".service")
		lines = append(lines, sourcedLine{App: "box", Service: svc, Time: at.UTC().Format(time.RFC3339Nano), Msg: cleanLogLine(msg), Level: levelOf(msg), at: at})
	}
	return lines
}

func (h *helperServer) boxLogs(since string, tail int) []sourcedLine {
	if _, err := exec.LookPath("journalctl"); err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	args := []string{"-u", "nexovirt-box", "-u", "nexovirt-box-helper", "-o", "json", "--no-pager", "-n", strconv.Itoa(tail)}
	if since != "" {
		t, _ := time.Parse(time.RFC3339Nano, since)
		args = append(args, "--since", t.Local().Format("2006-01-02 15:04:05"))
	}
	out, _ := exec.CommandContext(ctx, "journalctl", args...).Output()
	return parseJournal(out)
}

// logsAll returns the newest lines of one source (app id or "box") or of every source.
func (h *helperServer) logsAll(req helperRequest, _ func(string)) (any, error) {
	if req.Since != "" {
		if _, err := time.Parse(time.RFC3339Nano, req.Since); err != nil {
			return nil, uerr("The time to read logs from is not valid.")
		}
	}
	var apps []string
	if req.ID == "" || req.ID != "box" {
		entries, _ := os.ReadDir(h.appsDir)
		for _, e := range entries {
			if e.IsDir() && validID(e.Name()) == nil && (req.ID == "" || req.ID == e.Name()) && exists(h.appDir(e.Name())+"/app.json") {
				apps = append(apps, e.Name())
			}
		}
	}
	var all []sourcedLine
	if req.ID == "" || req.ID == "box" {
		all = append(all, h.boxLogs(req.Since, logsAllPerSource)...)
	}
	if needDocker() == nil {
		for _, id := range apps {
			raw, err := h.appLogs(helperRequest{ID: id, Tail: logsAllPerSource, Since: req.Since}, nil)
			if err != nil {
				continue
			}
			for _, l := range raw.(map[string]any)["lines"].([]logLine) {
				all = append(all, sourcedLine{App: id, Service: l.Service, Time: l.Time, Msg: l.Msg, Level: levelOf(l.Msg), at: l.at})
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
	if len(all) > logsAllMax {
		all = all[len(all)-logsAllMax:]
	}
	if all == nil {
		all = []sourcedLine{}
	}
	return map[string]any{"lines": all, "apps": apps}, nil
}

// filterLogs keeps lines matching the text (case-insensitive) and the level (and worse).
func filterLogs(lines []sourcedLine, q, level string) []sourcedLine {
	rank := map[string]int{"debug": 0, "info": 1, "warn": 2, "error": 3}
	min, byLevel := rank[level]
	q = strings.ToLower(strings.TrimSpace(q))
	out := []sourcedLine{}
	for _, l := range lines {
		if byLevel && rank[l.Level] < min {
			continue
		}
		if q != "" && !strings.Contains(strings.ToLower(l.Msg), q) && !strings.Contains(l.App+"/"+l.Service, q) {
			continue
		}
		out = append(out, l)
	}
	return out
}

func (a *App) logsSearch(w http.ResponseWriter, r *http.Request) {
	qv := r.URL.Query()
	app := qv.Get("app")
	if app != "" && app != "box" && validID(app) != nil {
		writeError(w, http.StatusNotFound, "This app does not exist.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	raw, err := a.helper.call(ctx, helperRequest{Action: "logs_all", ID: app, Since: qv.Get("since")}, nil)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	var res struct {
		Lines []sourcedLine `json:"lines"`
		Apps  []string      `json:"apps"`
	}
	json.Unmarshal(raw, &res)
	writeJSON(w, http.StatusOK, map[string]any{"lines": filterLogs(res.Lines, qv.Get("q"), qv.Get("level")), "apps": res.Apps})
}
