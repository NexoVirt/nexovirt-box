package main

// The root helper. The web process runs as an unprivileged user and asks this helper (same
// binary, `nexovirt-box helper`) for the few things that need root, over a unix socket that
// only the web user (checked with the peer credentials) may use. The helper only knows a fixed
// list of typed actions; there is no "run this command".
//
// Protocol: the client sends one JSON line {"action": ...}. The helper answers with JSON lines:
// {"type":"log","line":"..."} while it works, then {"type":"result","ok":true,"data":...} or
// {"type":"result","ok":false,"error":"friendly text"}. The console action answers with one
// result line that carries the pty master file descriptor (SCM_RIGHTS).

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

type helperRequest struct {
	Action     string            `json:"action"`
	Cols       uint16            `json:"cols,omitempty"`
	Rows       uint16            `json:"rows,omitempty"`
	ID         string            `json:"id,omitempty"`
	Op         string            `json:"op,omitempty"`
	Answers    map[string]string `json:"answers,omitempty"`
	Ports      map[string]int    `json:"ports,omitempty"`
	DeleteData bool              `json:"delete_data,omitempty"`
	Tail       int               `json:"tail,omitempty"`
	Password   string            `json:"password,omitempty"`
	Code       string            `json:"code,omitempty"`
	Session    string            `json:"session,omitempty"` // sha256 of the session cookie
	Proof      string            `json:"proof,omitempty"`
	HTTPPort   int               `json:"http_port,omitempty"`
	HTTPSPort  int               `json:"https_port,omitempty"`
	Email      string            `json:"email,omitempty"`
	Domain     string            `json:"domain,omitempty"`
	DataDir    string            `json:"data_dir,omitempty"`
	URL        string            `json:"url,omitempty"`
	Rules      []fwRule          `json:"rules,omitempty"`
	Confirm    string            `json:"confirm,omitempty"`
	Path       string            `json:"path,omitempty"`
	Path2      string            `json:"path2,omitempty"`
	Offset     int64             `json:"offset,omitempty"`
	Overwrite  bool              `json:"overwrite,omitempty"`
}

type helperMsg struct {
	Type  string          `json:"type"`
	Line  string          `json:"line,omitempty"`
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// userError is shown to the admin as is; any other error is logged and replaced.
type userError struct{ msg string }

func (e userError) Error() string { return e.msg }

func uerr(format string, a ...any) error { return userError{fmt.Sprintf(format, a...)} }

type helperServer struct {
	allowUID int    // uid of the web user (root is always allowed)
	appsDir  string // root-owned app directories
	webState string // web state dir (database, synced catalog; read only here)
	proofs   *proofKeeper
	mu       sync.Mutex
	appLocks map[string]*sync.Mutex
}

func (h *helperServer) peerAllowed(uid int) bool { return uid == 0 || uid == h.allowUID }

// listen creates the socket with mode 0660 and the web user's group (gid < 0 keeps it).
func (h *helperServer) listen(path string, gid int) (net.Listener, error) {
	os.Remove(path)
	old := syscall.Umask(0o117)
	ln, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err != nil {
		return nil, err
	}
	if gid >= 0 {
		if err := os.Chown(path, 0, gid); err != nil {
			ln.Close()
			return nil, err
		}
	}
	return ln, os.Chmod(path, 0o660)
}

func (h *helperServer) serve(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Printf("helper accept: %v", err)
			time.Sleep(100 * time.Millisecond)
			continue
		}
		go h.handle(c.(*net.UnixConn))
	}
}

func (h *helperServer) handle(c *net.UnixConn) {
	defer c.Close()
	uid, err := peerUID(c)
	if err != nil || !h.peerAllowed(uid) {
		log.Printf("helper: refused peer uid %d (%v)", uid, err)
		return
	}
	r := bufio.NewReader(io.LimitReader(c, 1<<20))
	line, err := r.ReadBytes('\n')
	if err != nil {
		return
	}
	var req helperRequest
	if err := json.Unmarshal(line, &req); err != nil {
		writeMsg(c, helperMsg{Type: "result", Error: "The request could not be read."})
		return
	}
	if req.Action == "console" {
		h.console(c, req)
		return
	}
	act, ok := h.actions()[req.Action]
	if !ok {
		writeMsg(c, helperMsg{Type: "result", Error: "This action is not allowed."})
		return
	}
	logf := func(s string) { writeMsg(c, helperMsg{Type: "log", Line: s}) }
	data, err := act(req, logf)
	if err != nil {
		msg := err.Error()
		var ue userError
		if !errors.As(err, &ue) {
			log.Printf("helper %s: %v", req.Action, err)
			msg = "The action failed on the server. Check the helper log (journalctl -u nexovirt-box-helper)."
		}
		writeMsg(c, helperMsg{Type: "result", Error: msg})
		return
	}
	raw, _ := json.Marshal(data)
	writeMsg(c, helperMsg{Type: "result", OK: true, Data: raw})
}

type helperAction func(req helperRequest, logf func(string)) (any, error)

// actions is the complete allowlist (plus "console", handled separately).
func (h *helperServer) actions() map[string]helperAction {
	return map[string]helperAction{
		"console_proof":    h.consoleProof,
		"docker_info":      h.dockerInfo,
		"docker_install":   h.dockerInstall,
		"apps_status":      h.appsStatus,
		"app_install":      h.appInstall,
		"app_action":       h.appAction,
		"app_uninstall":    h.appUninstall,
		"app_logs":         h.appLogs,
		"app_domain":       h.appDomain,
		"proxy_status":     h.proxyStatus,
		"proxy_setup":      h.proxySetup,
		"proxy_remove":     h.proxyRemove,
		"images_unused":    h.imagesUnused,
		"images_prune":     h.imagesPrune,
		"updates_check":    h.updatesCheck,
		"self_update":      h.selfUpdate,
		"firewall_status":  h.firewallStatus,
		"firewall_apply":   h.firewallApply,
		"firewall_confirm": h.firewallConfirm,
	}
}

func writeMsg(c net.Conn, m helperMsg) error {
	b, _ := json.Marshal(m)
	_, err := c.Write(append(b, '\n'))
	return err
}

// console starts a root login shell in a pty and hands the pty master to the web process.
// The shell ends when the web process closes the connection (or the shell exits).
func (h *helperServer) console(c *net.UnixConn, req helperRequest) {
	if !h.consoleAllowed(req) {
		writeMsg(c, helperMsg{Type: "result", Error: errProof.msg})
		return
	}
	shell, home, name := loginShell()
	cmd := exec.Command(shell, "-l")
	cmd.Dir = home
	cmd.Env = []string{
		"TERM=xterm-256color", "COLORTERM=truecolor", "HOME=" + home, "USER=" + name, "LOGNAME=" + name,
		"SHELL=" + shell, "LANG=" + envOr("LANG", "C.UTF-8"),
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
	}
	cols, rows := req.Cols, req.Rows
	if cols == 0 || cols > 1000 {
		cols = 80
	}
	if rows == 0 || rows > 1000 {
		rows = 24
	}
	tty, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: cols, Rows: rows})
	if err != nil {
		log.Printf("helper console: %v", err)
		writeMsg(c, helperMsg{Type: "result", Error: "The shell could not be started."})
		return
	}
	b, _ := json.Marshal(helperMsg{Type: "result", OK: true})
	_, _, err = c.WriteMsgUnix(append(b, '\n'), unix.UnixRights(int(tty.Fd())), nil)
	tty.Close() // the web process holds its own copy now
	if err != nil {
		cmd.Process.Kill()
		cmd.Wait()
		return
	}
	done := make(chan struct{})
	go func() {
		io.Copy(io.Discard, c) // returns when the web process closes the connection
		select {
		case <-done:
		default:
			syscall.Kill(-cmd.Process.Pid, syscall.SIGHUP)
			time.AfterFunc(2*time.Second, func() { syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
		}
	}()
	cmd.Wait()
	close(done)
}

// ---- client (web process side) ----

type helperClient struct{ socket string }

var errHelperDown = userError{"The NexoVirt Box helper service is not running. Start it with: systemctl start nexovirt-box-helper"}

func (hc *helperClient) dial() (*net.UnixConn, error) {
	c, err := net.DialTimeout("unix", hc.socket, 3*time.Second)
	if err != nil {
		log.Printf("helper dial: %v", err)
		return nil, errHelperDown
	}
	return c.(*net.UnixConn), nil
}

// call runs one action. onLog receives progress lines (may be nil).
func (hc *helperClient) call(ctx context.Context, req helperRequest, onLog func(string)) (json.RawMessage, error) {
	c, err := hc.dial()
	if err != nil {
		return nil, err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	if err := writeReq(c, req); err != nil {
		return nil, errHelperDown
	}
	sc := bufio.NewScanner(c)
	sc.Buffer(make([]byte, 64*1024), 8<<20)
	for sc.Scan() {
		var m helperMsg
		if json.Unmarshal(sc.Bytes(), &m) != nil {
			continue
		}
		if m.Type == "log" {
			if onLog != nil {
				onLog(m.Line)
			}
			continue
		}
		if !m.OK {
			return nil, userError{m.Error}
		}
		return m.Data, nil
	}
	return nil, userError{"The helper stopped before the action finished. Check the helper log."}
}

func writeReq(c net.Conn, req helperRequest) error {
	b, _ := json.Marshal(req)
	_, err := c.Write(append(b, '\n'))
	return err
}

// console asks for a root shell and returns the pty master plus the connection that keeps the
// shell alive (close both to end it).
func (hc *helperClient) console(session, proof string, cols, rows uint16) (*os.File, *net.UnixConn, error) {
	c, err := hc.dial()
	if err != nil {
		return nil, nil, err
	}
	if err := writeReq(c, helperRequest{Action: "console", Session: session, Proof: proof, Cols: cols, Rows: rows}); err != nil {
		c.Close()
		return nil, nil, errHelperDown
	}
	buf := make([]byte, 4096)
	oob := make([]byte, unix.CmsgSpace(4))
	c.SetReadDeadline(time.Now().Add(10 * time.Second))
	n, oobn, _, _, err := c.ReadMsgUnix(buf, oob)
	c.SetReadDeadline(time.Time{})
	if err != nil {
		c.Close()
		return nil, nil, errHelperDown
	}
	var m helperMsg
	json.Unmarshal([]byte(strings.TrimSpace(string(buf[:n]))), &m)
	if !m.OK {
		c.Close()
		if m.Error == "" {
			m.Error = "The shell could not be started."
		}
		return nil, nil, userError{m.Error}
	}
	msgs, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil || len(msgs) == 0 {
		c.Close()
		return nil, nil, userError{"The shell could not be started."}
	}
	fds, err := unix.ParseUnixRights(&msgs[0])
	if err != nil || len(fds) == 0 {
		c.Close()
		return nil, nil, userError{"The shell could not be started."}
	}
	return os.NewFile(uintptr(fds[0]), "pty"), c, nil
}
