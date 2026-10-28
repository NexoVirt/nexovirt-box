package main

// App logs for the Logs page: per compose service, with Docker's timestamps, newest last.
// The page polls with since= (the newest timestamp it has) to follow the logs live.

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	logsTailDefault = 500
	logsTailMax     = 10000
	logsBytesMax    = 4 << 20 // per service and request
)

type logLine struct {
	Service string `json:"s"`
	Time    string `json:"t"` // RFC 3339 with nanoseconds, as Docker prints it
	Msg     string `json:"m"`
	at      time.Time
}

// parseLogOutput reads `docker compose logs --timestamps --no-log-prefix` output of one service.
func parseLogOutput(out []byte, service string) []logLine {
	var lines []logLine
	var last time.Time
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		raw := cleanLogLine(sc.Text())
		ts, msg, ok := strings.Cut(raw, " ")
		at, err := time.Parse(time.RFC3339Nano, ts)
		if !ok || err != nil {
			if raw == "" {
				continue
			}
			ts, msg, at = "", raw, last // a line without a time (Docker's own warnings) stays in place
		}
		last = at
		lines = append(lines, logLine{Service: service, Time: ts, Msg: msg, at: at})
	}
	return lines
}

// mergeLogs orders lines of several services by time (stable, so one service keeps its order)
// and keeps the newest max.
func mergeLogs(parts [][]logLine, max int) []logLine {
	var all []logLine
	for _, p := range parts {
		all = append(all, p...)
	}
	sort.SliceStable(all, func(i, j int) bool { return all[i].at.Before(all[j].at) })
	if len(all) > max {
		all = all[len(all)-max:]
	}
	return all
}

func (h *helperServer) appLogs(req helperRequest, logf func(string)) (any, error) {
	if err := validID(req.ID); err != nil {
		return nil, err
	}
	if err := needDocker(); err != nil {
		return nil, err
	}
	if _, err := h.readMeta(req.ID); err != nil {
		return nil, err
	}
	tail := req.Tail
	if tail <= 0 {
		tail = logsTailDefault
	}
	tail = min(tail, logsTailMax)
	var since string
	if req.Since != "" {
		t, err := time.Parse(time.RFC3339Nano, req.Since)
		if err != nil {
			return nil, uerr("The time to read logs from is not valid.")
		}
		since = t.UTC().Format(time.RFC3339Nano)
	}
	dir := h.appDir(req.ID)
	cs, _, _ := composeStatus(dir, req.ID)
	services := []string{}
	for _, c := range cs {
		if !slices.Contains(services, c.Service) {
			services = append(services, c.Service)
		}
	}
	sort.Strings(services)
	pick := services
	if req.Service != "" {
		if !slices.Contains(services, req.Service) {
			return nil, uerr("This app has no service called %s.", req.Service)
		}
		pick = []string{req.Service}
	}
	parts := make([][]logLine, len(pick))
	failed := 0
	for i, svc := range pick {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		args := []string{"logs", "--no-color", "--no-log-prefix", "--timestamps", "--tail", strconv.Itoa(tail)}
		if since != "" {
			args = append(args, "--since", since)
		}
		c := compose(dir, req.ID, append(args, "--", svc)...)
		cmd := exec.CommandContext(ctx, c.Path, c.Args[1:]...)
		cmd.Dir = c.Dir
		out, err := cmd.Output() // stdout only: compose prints its own warnings to stderr
		cancel()
		if err != nil && len(out) == 0 {
			failed++
			continue
		}
		if len(out) > logsBytesMax {
			out = out[len(out)-logsBytesMax:]
		}
		parts[i] = parseLogOutput(out, svc)
	}
	if failed > 0 && failed == len(pick) {
		return nil, uerr("The logs could not be read.")
	}
	return map[string]any{"services": services, "lines": mergeLogs(parts, tail)}, nil
}
