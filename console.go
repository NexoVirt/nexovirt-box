package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/user"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// console pipes a root shell (started by the helper, which hands over the pty) over a
// WebSocket. Binary frames carry terminal bytes both ways; text frames from the browser are
// control messages ({"type":"resize","cols":N,"rows":N}). Opening it needs a password proof
// from the last few minutes, and it closes when the session signs out.
func (a *App) console(w http.ResponseWriter, r *http.Request) {
	token := sessionToken(r)
	if !a.store.sessionFresh(token, reauthWindow) {
		writeJSON(w, http.StatusForbidden, map[string]any{"error": "Enter your password to open the console.", "reauth": true})
		return
	}
	tty, hconn, herr := a.helper.console(uint16(atoiOr(r.URL.Query().Get("cols"), 80)), uint16(atoiOr(r.URL.Query().Get("rows"), 24)))

	// Accept checks the Origin header against the Host by default, so other sites cannot
	// open a console with the visitor's cookie.
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		if herr == nil {
			tty.Close()
			hconn.Close()
		}
		return
	}
	defer conn.CloseNow()
	if herr != nil {
		conn.Close(websocket.StatusInternalError, herr.Error())
		return
	}
	defer hconn.Close()
	defer tty.Close()
	conn.SetReadLimit(1 << 20)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	remove := a.consoles.add(hashToken(token), cancel)
	defer remove()
	started := time.Now()
	a.audit(r, "console.open", "")
	uid, _ := a.currentUser(r)
	name, ip := a.store.username(uid), a.clientIP(r) // the session may be gone when it closes
	defer func() {
		a.store.audit(name, ip, "console.close", "after "+time.Since(started).Round(time.Second).String())
	}()

	// shell -> browser
	go func() {
		defer cancel()
		buf := make([]byte, 32*1024)
		for {
			n, err := tty.Read(buf)
			if n > 0 {
				wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
				werr := conn.Write(wctx, websocket.MessageBinary, buf[:n])
				wcancel()
				if werr != nil {
					return
				}
			}
			if err != nil {
				conn.Close(websocket.StatusNormalClosure, "The shell has ended.")
				return
			}
		}
	}()

	// browser -> shell
	for {
		typ, data, err := conn.Read(ctx)
		if err != nil {
			if ctx.Err() != nil && r.Context().Err() == nil {
				conn.Close(websocket.StatusNormalClosure, "You signed out.")
			}
			return
		}
		if typ == websocket.MessageBinary {
			if _, err := tty.Write(data); err != nil {
				return
			}
			continue
		}
		var msg struct {
			Type string `json:"type"`
			Cols uint16 `json:"cols"`
			Rows uint16 `json:"rows"`
		}
		if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 && msg.Cols <= 1000 && msg.Rows <= 1000 {
			pty.Setsize(tty, &pty.Winsize{Cols: msg.Cols, Rows: msg.Rows})
		}
	}
}

// consoleRegistry tracks open consoles per session, so signing out closes them.
type consoleRegistry struct {
	mu   sync.Mutex
	next int
	m    map[string]map[int]func()
}

func newConsoleRegistry() *consoleRegistry { return &consoleRegistry{m: map[string]map[int]func(){}} }

func (c *consoleRegistry) add(session string, cancel func()) (remove func()) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.next++
	id := c.next
	if c.m[session] == nil {
		c.m[session] = map[int]func(){}
	}
	c.m[session][id] = cancel
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		delete(c.m[session], id)
		if len(c.m[session]) == 0 {
			delete(c.m, session)
		}
	}
}

func (c *consoleRegistry) closeAll(session string) {
	c.mu.Lock()
	fns := c.m[session]
	delete(c.m, session)
	c.mu.Unlock()
	for _, f := range fns {
		f()
	}
}

// loginShell returns the service user's shell from /etc/passwd, falling back to bash or sh.
func loginShell() (shell, home, name string) {
	u, err := user.Current()
	if err == nil {
		home, name = u.HomeDir, u.Username
	}
	if home == "" {
		home = "/"
	}
	if f, err := os.Open("/etc/passwd"); err == nil {
		defer f.Close()
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			parts := strings.Split(sc.Text(), ":")
			if len(parts) == 7 && u != nil && parts[2] == u.Uid {
				shell = parts[6]
			}
		}
	}
	if shell == "" || strings.HasSuffix(shell, "nologin") || strings.HasSuffix(shell, "false") {
		shell = envOr("SHELL", "")
	}
	for _, s := range []string{shell, "/bin/bash", "/bin/sh"} {
		if s != "" {
			if _, err := os.Stat(s); err == nil {
				return s, home, name
			}
		}
	}
	return "/bin/sh", home, name
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func atoiOr(s string, def int) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 1000 {
		return def
	}
	return n
}
