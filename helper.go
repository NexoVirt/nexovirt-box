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
	Retry      bool              `json:"retry,omitempty"`
	// AllowDockerSocket: the admin confirmed the warning for an app marked docker_socket.
	AllowDockerSocket bool     `json:"allow_docker_socket,omitempty"`
	URL               string   `json:"url,omitempty"`
	Rules             []fwRule `json:"rules,omitempty"`
	Confirm           string   `json:"confirm,omitempty"`
	Path              string   `json:"path,omitempty"`
	Path2             string   `json:"path2,omitempty"`
	Offset            int64    `json:"offset,omitempty"`
	Overwrite         bool     `json:"overwrite,omitempty"`
	// Backups and own apps.
	Target   *backupTarget `json:"target,omitempty"`
	TargetID string        `json:"target_id,omitempty"`
	Name     string        `json:"name,omitempty"`
	NewID    string        `json:"new_id,omitempty"`
	Keep     keepPolicy    `json:"keep,omitempty"`
	Key      string        `json:"key,omitempty"`
	Compose  string        `json:"compose,omitempty"`
	Env      string        `json:"env,omitempty"`
	Icon     string        `json:"icon,omitempty"`
	// System updates, disks and shares.
	Packages []string    `json:"packages,omitempty"`
	Share    *sambaShare `json:"share,omitempty"`
	Export   *nfsExport  `json:"export,omitempty"`
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
	etcDir   string // /etc/nexovirt-box (backup key and targets live in backup/)
	proofs   *proofKeeper
	mu       sync.Mutex
	appLocks map[string]*sync.Mutex
	busy     map[string]string // app id -> long action running now
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
	if fa, ok := h.fdActions()[req.Action]; ok {
		data, f, err := fa(req)
		if err != nil {
			writeMsg(c, helperMsg{Type: "result", Error: friendlyHelperErr(req.Action, err)})
			return
		}
		raw, _ := json.Marshal(data)
		b, _ := json.Marshal(helperMsg{Type: "result", OK: true, Data: raw})
		c.WriteMsgUnix(append(b, '\n'), unix.UnixRights(int(f.Fd())), nil)
		f.Close() // the web process holds its own copy now
		return
	}
	if ca, ok := h.ctxActions()[req.Action]; ok {
		// The action stops when the web process goes away (restart, redeploy, watchdog): the
		// app is then free again and its job is marked interrupted.
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { io.Copy(io.Discard, r); cancel() }()
		logf := func(s string) { writeMsg(c, helperMsg{Type: "log", Line: s}) }
		data, err := ca(ctx, req, logf)
		if err != nil {
			writeMsg(c, helperMsg{Type: "result", Error: friendlyHelperErr(req.Action, err)})
			return
		}
		raw, _ := json.Marshal(data)
		writeMsg(c, helperMsg{Type: "result", OK: true, Data: raw})
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
		writeMsg(c, helperMsg{Type: "result", Error: friendlyHelperErr(req.Action, err)})
		return
	}
	raw, _ := json.Marshal(data)
	writeMsg(c, helperMsg{Type: "result", OK: true, Data: raw})
}

func friendlyHelperErr(action string, err error) string {
	var ue userError
	if errors.As(err, &ue) {
		return ue.msg
	}
	log.Printf("helper %s: %v", action, err)
	return "The action failed on the server. Check the helper log (journalctl -u nexovirt-box-helper)."
}

type helperAction func(req helperRequest, logf func(string)) (any, error)

// fdActions answer with an open file descriptor (file downloads and uploads).
func (h *helperServer) fdActions() map[string]func(helperRequest) (any, *os.File, error) {
	return map[string]func(helperRequest) (any, *os.File, error){
		"files_open_read":  h.filesOpenRead,
		"files_open_write": h.filesOpenWrite,
		"files_zip":        h.filesZip,
	}
}

// ctxActions are the long app actions; they are cancelled when the caller disconnects.
func (h *helperServer) ctxActions() map[string]func(context.Context, helperRequest, func(string)) (any, error) {
	return map[string]func(context.Context, helperRequest, func(string)) (any, error){
		"app_install":     h.appInstall,
		"app_action":      h.appAction,
		"app_uninstall":   h.appUninstall,
		"backup_run":      h.backupRun,
		"backup_restore":  h.backupRestore,
		"backup_verify":   h.backupVerify,
		"custom_app_save": h.customAppSave,
		"pkg_install":     h.pkgInstall,
		"auto_updates":    h.autoUpdatesSet,
	}
}

// actions is the complete allowlist (plus "console" and ctxActions, handled separately).
func (h *helperServer) actions() map[string]helperAction {
	return map[string]helperAction{
		"console_proof":        h.consoleProof,
		"docker_info":          h.dockerInfo,
		"docker_install":       h.dockerInstall,
		"apps_status":          h.appsStatus,
		"app_credentials":      h.appCredentials,
		"app_logs":             h.appLogs,
		"app_domain":           h.appDomain,
		"proxy_status":         h.proxyStatus,
		"proxy_setup":          h.proxySetup,
		"proxy_remove":         h.proxyRemove,
		"images_unused":        h.imagesUnused,
		"images_prune":         h.imagesPrune,
		"updates_check":        h.updatesCheck,
		"self_update":          h.selfUpdate,
		"firewall_status":      h.firewallStatus,
		"firewall_apply":       h.firewallApply,
		"firewall_confirm":     h.firewallConfirm,
		"files_list":           h.filesList,
		"files_mkdir":          h.filesMkdir,
		"files_rename":         h.filesRename,
		"files_delete":         h.filesDelete,
		"files_finish":         h.filesUploadFinish,
		"files_roots_set":      h.filesRootsSet,
		"files_copy":           h.filesCopy,
		"files_size":           h.filesSize,
		"backup_key":           h.backupKey,
		"backup_targets":       h.backupTargets,
		"backup_target_save":   h.backupTargetSave,
		"backup_target_delete": h.backupTargetDelete,
		"backup_list":          h.backupList,
		"backup_delete":        h.backupDelete,
		"backup_prune":         h.backupPrune,
		"custom_app_get":       h.customAppGet,
		"files_stat":           h.filesStat,
		// System updates, disks and shares (sysupdates.go, disks.go, shares.go).
		"apt_status":         h.aptStatus,
		"apt_refresh":        h.aptRefresh,
		"apt_simulate":       h.aptSimulate,
		"apt_upgrade_start":  h.aptUpgradeStart,
		"apt_upgrade_status": h.aptUpgradeStatus,
		"system_reboot":      h.systemReboot,
		"disks_list":         h.disksList,
		"smart_selftest":     h.smartSelftest,
		"shares_status":      h.sharesStatus,
		"samba_share_save":   h.sambaShareSave,
		"samba_share_delete": h.sambaShareDelete,
		"samba_user_save":    h.sambaUserSave,
		"samba_user_delete":  h.sambaUserDelete,
		"samba_handover":     h.sambaHandover,
		"nfs_export_save":    h.nfsExportSave,
		"nfs_export_delete":  h.nfsExportDelete,
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
	return nil, errHelperStopped
}

// errHelperStopped: the helper went away mid-action (restart, redeploy). The job counts as
// interrupted and the app can be retried.
var errHelperStopped = userError{"The action was interrupted because the Box helper restarted. Try it again."}

func writeReq(c net.Conn, req helperRequest) error {
	b, _ := json.Marshal(req)
	_, err := c.Write(append(b, '\n'))
	return err
}

// console asks for a root shell and returns the pty master plus the connection that keeps the
// shell alive (close both to end it).
func (hc *helperClient) console(session, proof string, cols, rows uint16) (*os.File, *net.UnixConn, error) {
	_, f, c, err := hc.callFD(helperRequest{Action: "console", Session: session, Proof: proof, Cols: cols, Rows: rows}, "The shell could not be started.")
	return f, c, err
}

// openFile runs an fd action (files_open_read / files_open_write) and returns the file.
func (hc *helperClient) openFile(req helperRequest) (json.RawMessage, *os.File, error) {
	data, f, c, err := hc.callFD(req, "The file could not be opened.")
	if c != nil {
		c.Close()
	}
	return data, f, err
}

// callFD sends one request and reads a result line that carries a file descriptor.
func (hc *helperClient) callFD(req helperRequest, failMsg string) (json.RawMessage, *os.File, *net.UnixConn, error) {
	c, err := hc.dial()
	if err != nil {
		return nil, nil, nil, err
	}
	if err := writeReq(c, req); err != nil {
		c.Close()
		return nil, nil, nil, errHelperDown
	}
	buf := make([]byte, 64*1024)
	oob := make([]byte, unix.CmsgSpace(4))
	c.SetReadDeadline(time.Now().Add(15 * time.Second))
	n, oobn, _, _, err := c.ReadMsgUnix(buf, oob)
	c.SetReadDeadline(time.Time{})
	if err != nil {
		c.Close()
		return nil, nil, nil, errHelperDown
	}
	var m helperMsg
	json.Unmarshal([]byte(strings.TrimSpace(string(buf[:n]))), &m)
	if !m.OK {
		c.Close()
		if m.Error == "" {
			m.Error = failMsg
		}
		return nil, nil, nil, userError{m.Error}
	}
	msgs, err := unix.ParseSocketControlMessage(oob[:oobn])
	if err != nil || len(msgs) == 0 {
		c.Close()
		return nil, nil, nil, userError{failMsg}
	}
	fds, err := unix.ParseUnixRights(&msgs[0])
	if err != nil || len(fds) == 0 {
		c.Close()
		return nil, nil, nil, userError{failMsg}
	}
	return m.Data, os.NewFile(uintptr(fds[0]), "helper-fd"), c, nil
}
