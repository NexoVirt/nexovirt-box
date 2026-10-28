package main

import (
	"bufio"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/exec"
	"os/user"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// console opens a login shell of the service user (root on a normal install) in a pty and
// pipes it over a WebSocket. Binary frames carry terminal bytes both ways; text frames from
// the browser are control messages ({"type":"resize","cols":N,"rows":N}).
func (a *App) console(w http.ResponseWriter, r *http.Request) {
	// Accept checks the Origin header against the Host by default, so other sites cannot
	// open a console with the visitor's cookie.
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.CloseNow()
	conn.SetReadLimit(1 << 20)

	shell, home, name := loginShell()
	cmd := exec.Command(shell, "-l")
	cmd.Dir = home
	cmd.Env = []string{
		"TERM=xterm-256color",
		"COLORTERM=truecolor",
		"HOME=" + home,
		"USER=" + name,
		"LOGNAME=" + name,
		"SHELL=" + shell,
		"LANG=" + envOr("LANG", "C.UTF-8"),
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	size := &pty.Winsize{Cols: uint16(atoiOr(r.URL.Query().Get("cols"), 80)), Rows: uint16(atoiOr(r.URL.Query().Get("rows"), 24))}
	tty, err := pty.StartWithSize(cmd, size)
	if err != nil {
		log.Printf("console: %v", err)
		conn.Close(websocket.StatusInternalError, "The shell could not be started.")
		return
	}
	defer func() {
		tty.Close()
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		cmd.Wait()
	}()

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

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
		if json.Unmarshal(data, &msg) == nil && msg.Type == "resize" && msg.Cols > 0 && msg.Rows > 0 {
			pty.Setsize(tty, &pty.Winsize{Cols: msg.Cols, Rows: msg.Rows})
		}
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
