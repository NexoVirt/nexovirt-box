package box

// System updates (Debian/Ubuntu apt), root side. Box refreshes the package lists, lists what can
// be upgraded, simulates a selection (so dependencies are picked up and removals refused) and
// runs the real upgrade DETACHED in its own systemd unit, so it survives a restart of Box or the
// helper. The log and the exit code land in /var/log/nexovirt-box. Package names are validated
// strictly and passed as separate arguments, never through a shell string.

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var (
	aptLogDir    = "/var/log/nexovirt-box"
	aptUnit      = "nexovirt-box-apt"
	autoDropIn   = "/etc/apt/apt.conf.d/52nexovirt-box-auto-updates"
	pkgNameRe    = regexp.MustCompile(`^[a-z0-9][a-z0-9+.-]{0,127}$`)
	maxSelection = 500
)

func aptLog() string  { return filepath.Join(aptLogDir, "apt-upgrade.log") }
func aptExit() string { return filepath.Join(aptLogDir, "apt-upgrade.exit") }

// validPackages checks every name (Debian policy: lowercase, digits, + - .) and drops duplicates.
func validPackages(names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, uerr("Choose at least one package.")
	}
	if len(names) > maxSelection {
		return nil, uerr("Choose at most %d packages at once.", maxSelection)
	}
	seen := map[string]bool{}
	out := []string{}
	for _, n := range names {
		if !pkgNameRe.MatchString(n) {
			return nil, uerr("%q is not a valid package name.", n)
		}
		if !seen[n] {
			seen[n] = true
			out = append(out, n)
		}
	}
	return out, nil
}

type aptPackage struct {
	Name      string `json:"name"`
	Current   string `json:"current"`
	Candidate string `json:"candidate"`
	Suite     string `json:"suite"`
	Arch      string `json:"arch"`
	Security  bool   `json:"security"`
}

// "libc6/stable-security 2.41-12+deb13u1 amd64 [upgradable from: 2.41-12]"
var upgradableRe = regexp.MustCompile(`^([a-z0-9][a-z0-9+.-]*)/(\S+) (\S+) (\S+) \[upgradable from: ([^\]]+)\]`)

func parseUpgradable(out string) []aptPackage {
	list := []aptPackage{}
	for _, l := range strings.Split(out, "\n") {
		m := upgradableRe.FindStringSubmatch(strings.TrimSpace(l))
		if m == nil {
			continue
		}
		list = append(list, aptPackage{Name: m[1], Suite: m[2], Candidate: m[3], Arch: m[4], Current: m[5],
			Security: strings.Contains(m[2], "-security")})
	}
	return list
}

type aptSimulation struct {
	Upgrade []string `json:"upgrade"`
	Install []string `json:"install"` // new packages the upgrade pulls in
	Remove  []string `json:"remove"`
	Kernel  bool     `json:"kernel"`
}

// "Inst libc6 [2.41-12] (2.41-12+deb13u1 ...)" upgrades, "Inst foo (1.0 ...)" is new, "Remv foo [1.0]".
func parseSimulation(out string) aptSimulation {
	s := aptSimulation{Upgrade: []string{}, Install: []string{}, Remove: []string{}}
	for _, l := range strings.Split(out, "\n") {
		f := strings.Fields(l)
		if len(f) < 2 || !pkgNameRe.MatchString(strings.SplitN(f[1], ":", 2)[0]) {
			continue
		}
		name := strings.SplitN(f[1], ":", 2)[0]
		switch f[0] {
		case "Inst":
			if len(f) > 2 && strings.HasPrefix(f[2], "[") {
				s.Upgrade = append(s.Upgrade, name)
			} else {
				s.Install = append(s.Install, name)
			}
			if strings.HasPrefix(name, "linux-image-") {
				s.Kernel = true
			}
		case "Remv", "Purg":
			s.Remove = append(s.Remove, name)
		}
	}
	return s
}

// aptErrors picks the error and warning lines of an apt-get update and names the main reason.
func aptErrors(out string) ([]string, string) {
	var lines []string
	reason := ""
	seen := map[string]bool{}
	ls := strings.Split(out, "\n")
	for i, l := range ls {
		t := strings.TrimSpace(l)
		if !(strings.HasPrefix(t, "E:") || strings.HasPrefix(t, "W:") || strings.HasPrefix(t, "Err:")) || seen[t] {
			continue
		}
		seen[t] = true
		// Missing translations (flat file repositories) are harmless: apt-get update still succeeds.
		if strings.HasPrefix(t, "Err:") && strings.Contains(t, " Translation-") {
			continue
		}
		lines = append(lines, t)
		ctx := t
		if strings.HasPrefix(t, "Err:") && i+1 < len(ls) {
			next := strings.TrimSpace(ls[i+1])
			lines = append(lines, next)
			ctx += " " + next
		}
		r := ""
		switch {
		case strings.Contains(ctx, "401") || strings.Contains(ctx, "403") || strings.Contains(ctx, "Unauthorized") || strings.Contains(ctx, "Forbidden"):
			r = "auth"
		case strings.Contains(ctx, "NO_PUBKEY") || strings.Contains(ctx, "EXPKEYSIG") || strings.Contains(ctx, "not signed"):
			r = "key"
		case strings.Contains(ctx, "Temporary failure resolving") || strings.Contains(ctx, "Could not resolve") || strings.Contains(ctx, "Could not connect") || strings.Contains(ctx, "timed out"):
			r = "network"
		case strings.Contains(ctx, "does not have a Release file") || strings.Contains(ctx, "404"):
			r = "release"
		case strings.Contains(ctx, "Could not get lock"):
			r = "lock"
		}
		if reason == "" && r != "" {
			reason = r
		}
	}
	if reason == "" && len(lines) > 0 {
		reason = "other"
	}
	return lines, reason
}

// The reason codes the UI explains.
var aptReasons = map[string]string{
	"auth":    "A repository refused access (401/403). It usually needs a subscription or is set up wrong.",
	"key":     "A repository's signing key is missing or expired.",
	"network": "A repository could not be reached. Check the server's internet connection and DNS.",
	"release": "A repository does not exist for this release (404). It may be outdated in the sources list.",
	"lock":    "Another package tool is running right now. Try again when it has finished.",
	"other":   "apt reported a problem with the package sources.",
}

// autoUpdateCfg is the time window and restart policy of automatic updates (Box's own files).
type autoUpdateCfg struct {
	From     string `json:"from"`      // HH:MM, start of the install window
	To       string `json:"to"`        // HH:MM, end of the window
	Reboot   string `json:"reboot"`    // never | needed
	RebootAt string `json:"reboot_at"` // HH:MM, when a needed restart happens
}

var (
	autoTimerDropIn = "/etc/systemd/system/apt-daily-upgrade.timer.d/52nexovirt-box.conf"
	autoCfgPath     = "/etc/nexovirt-box/auto-updates.json"
)

func defaultAutoCfg() autoUpdateCfg {
	return autoUpdateCfg{From: "04:00", To: "05:00", Reboot: "never", RebootAt: "04:30"}
}

func (c autoUpdateCfg) validate() error {
	if !hhmmRe.MatchString(c.From) || !hhmmRe.MatchString(c.To) || c.From == c.To || !hhmmRe.MatchString(c.RebootAt) {
		return uerr("Use times like 04:00 for the update window and the restart.")
	}
	if c.Reboot != "never" && c.Reboot != "needed" {
		return uerr("Choose when the server may restart.")
	}
	return nil
}

// windowMinutes is the length of [from, to), across midnight when to <= from.
func (c autoUpdateCfg) windowMinutes() int {
	f, t := minutes(c.From), minutes(c.To)
	if t <= f {
		t += 24 * 60
	}
	return t - f
}

func autoUpdatesDropIn(c autoUpdateCfg) string {
	s := "// Managed by NexoVirt Box (Settings: automatic security updates). Remove it there.\n" +
		"APT::Periodic::Update-Package-Lists \"1\";\nAPT::Periodic::Unattended-Upgrade \"1\";\n"
	if c.Reboot == "needed" {
		s += "Unattended-Upgrade::Automatic-Reboot \"true\";\nUnattended-Upgrade::Automatic-Reboot-Time \"" + c.RebootAt + "\";\n"
	} else {
		s += "Unattended-Upgrade::Automatic-Reboot \"false\";\n"
	}
	return s
}

// autoTimer moves apt-daily-upgrade (which runs unattended-upgrades) into the window.
func autoTimer(c autoUpdateCfg) string {
	return "# Managed by NexoVirt Box: automatic updates run in this window.\n[Timer]\nOnCalendar=\nOnCalendar=*-*-* " + c.From +
		":00\nRandomizedDelaySec=" + strconv.Itoa(max(c.windowMinutes()-5, 1)) + "m\nPersistent=true\n"
}

// lastAutoRun reads when unattended-upgrades last ran and how it ended.
func lastAutoRun() map[string]any {
	out := map[string]any{"at": int64(0), "result": ""}
	if st, err := os.Stat("/var/lib/apt/periodic/unattended-upgrades-stamp"); err == nil {
		out["at"] = st.ModTime().Unix()
	}
	b, err := os.ReadFile("/var/log/unattended-upgrades/unattended-upgrades.log")
	if err != nil {
		return out
	}
	if len(b) > 256<<10 {
		b = b[len(b)-256<<10:]
	}
	out["result"] = lastAutoResult(string(b))
	return out
}

// lastAutoResult picks the outcome of the newest run from the unattended-upgrades log.
func lastAutoResult(log string) string {
	lines := strings.Split(strings.TrimSpace(log), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		_, msg, ok := strings.Cut(l, " INFO ")
		if !ok {
			if _, m, ok2 := strings.Cut(l, " ERROR "); ok2 {
				return "Error: " + strings.TrimSpace(m)
			}
			if _, m, ok2 := strings.Cut(l, " WARNING "); ok2 {
				return "Warning: " + strings.TrimSpace(m)
			}
			continue
		}
		msg = strings.TrimSpace(msg)
		switch {
		case strings.HasPrefix(msg, "All upgrades installed"), strings.HasPrefix(msg, "No packages found that can be upgraded"),
			strings.HasPrefix(msg, "Packages that will be upgraded"), strings.HasPrefix(msg, "Starting unattended upgrades"):
			return msg
		}
	}
	return ""
}

func aptCmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Env = []string{"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin", "LC_ALL=C",
		"DEBIAN_FRONTEND=noninteractive", "HOME=/root"}
	return cmd
}

func hasAptGet() bool { _, err := exec.LookPath("apt-get"); return err == nil }

var errNoApt = userError{"System updates need apt (Debian or Ubuntu). This server does not have it."}

// ---- helper actions ----

func (h *helperServer) aptStatus(req helperRequest, logf func(string)) (any, error) {
	if !hasAptGet() {
		return nil, errNoApt
	}
	return aptState(), nil
}

func aptState() map[string]any {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	out, _ := aptCmd(ctx, "apt", "list", "--upgradable").Output()
	var lists int64
	if st, err := os.Stat("/var/lib/apt/lists"); err == nil {
		lists = st.ModTime().Unix()
	}
	if st, err := os.Stat("/var/lib/apt/periodic/update-success-stamp"); err == nil && st.ModTime().Unix() > lists {
		lists = st.ModTime().Unix()
	}
	return map[string]any{
		"packages": parseUpgradable(string(out)), "lists_at": lists, "reboot_required": rebootRequired(),
		"running": upgradeRunning(), "auto": autoUpdatesState(),
	}
}

func (h *helperServer) aptRefresh(req helperRequest, logf func(string)) (any, error) {
	if !hasAptGet() {
		return nil, errNoApt
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	out, err := aptCmd(ctx, "apt-get", "update").CombinedOutput()
	if ctx.Err() != nil {
		return nil, uerr("Refreshing the package lists took longer than 150 seconds and was stopped. Check the server's internet connection.")
	}
	lines, reason := aptErrors(string(out))
	if err != nil && len(lines) == 0 {
		lines = []string{strings.TrimSpace(lastLines(string(out), 5))}
		reason = "other"
	}
	st := aptState()
	st["errors"], st["reason"], st["reason_text"] = lines, reason, aptReasons[reason]
	return st, nil
}

// simulate runs apt-get -s for "all" (dist-upgrade) or a selection (only upgrades of installed packages).
func simulate(op string, pkgs []string) (aptSimulation, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	args := []string{"apt-get", "-s", "-o", "Debug::NoLocking=1", "dist-upgrade"}
	if op == "selected" {
		args = append([]string{"apt-get", "-s", "-o", "Debug::NoLocking=1", "install", "--only-upgrade", "--"}, pkgs...)
	}
	out, err := aptCmd(ctx, args...).CombinedOutput()
	if err != nil {
		lines, _ := aptErrors(string(out))
		msg := strings.Join(lines, " ")
		if msg == "" {
			msg = lastLines(string(out), 3)
		}
		return aptSimulation{}, uerr("apt could not plan this update: %s", strings.TrimSpace(msg))
	}
	return parseSimulation(string(out)), nil
}

func readSelection(req helperRequest) (string, []string, error) {
	switch req.Op {
	case "all":
		return "all", nil, nil
	case "selected":
		pkgs, err := validPackages(req.Packages)
		return "selected", pkgs, err
	}
	return "", nil, uerr("Choose all updates or a selection.")
}

func (h *helperServer) aptSimulate(req helperRequest, logf func(string)) (any, error) {
	if !hasAptGet() {
		return nil, errNoApt
	}
	op, pkgs, err := readSelection(req)
	if err != nil {
		return nil, err
	}
	s, err := simulate(op, pkgs)
	if err != nil {
		return nil, err
	}
	// Upgrades apt added on its own (dependencies) are reported separately so the UI ticks them.
	chosen := map[string]bool{}
	for _, p := range pkgs {
		chosen[p] = true
	}
	added := []string{}
	for _, p := range s.Upgrade {
		if op == "selected" && !chosen[p] {
			added = append(added, p)
		}
	}
	return map[string]any{"simulation": s, "added": added}, nil
}

func (h *helperServer) aptUpgradeStart(req helperRequest, logf func(string)) (any, error) {
	if !hasAptGet() {
		return nil, errNoApt
	}
	op, pkgs, err := readSelection(req)
	if err != nil {
		return nil, err
	}
	if upgradeRunning() {
		return nil, uerr("An update is already running.")
	}
	s, err := simulate(op, pkgs) // never trust the browser's plan: check removals here again
	if err != nil {
		return nil, err
	}
	if len(s.Remove) > 0 {
		return nil, uerr("This update would remove packages (%s). Box does not remove packages; run it in the console if you want that.", strings.Join(s.Remove, ", "))
	}
	if len(s.Upgrade)+len(s.Install) == 0 {
		return nil, uerr("There is nothing to update.")
	}
	if op == "selected" {
		pkgs = s.Upgrade // the selection plus the upgrades it depends on
	}
	if err := os.MkdirAll(aptLogDir, 0o700); err != nil {
		return nil, err
	}
	os.Remove(aptExit())
	if err := os.WriteFile(aptLog(), nil, 0o600); err != nil {
		return nil, err
	}
	aptArgs := []string{"-y", "-o", "Dpkg::Options::=--force-confdef", "-o", "Dpkg::Options::=--force-confold"}
	if op == "all" {
		aptArgs = append(aptArgs, "dist-upgrade")
	} else {
		aptArgs = append(append(aptArgs, "install", "--only-upgrade", "--"), pkgs...)
	}
	// $1 = log, $2 = exit file, the rest goes to apt-get as separate arguments.
	script := `log="$1"; ex="$2"; shift 2; apt-get "$@" >>"$log" 2>&1; echo $? >"$ex"`
	args := []string{"systemd-run", "--unit=" + aptUnit, "--collect", "--quiet", "--description=NexoVirt Box system update",
		"--setenv=DEBIAN_FRONTEND=noninteractive", "--setenv=LC_ALL=C", "--setenv=NEEDRESTART_MODE=a",
		"/bin/sh", "-c", script, "sh", aptLog(), aptExit()}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, err := aptCmd(ctx, append(args, aptArgs...)...).CombinedOutput(); err != nil {
		return nil, uerr("The update could not be started: %s", strings.TrimSpace(lastLines(string(out), 2)))
	}
	return map[string]any{"started": true, "packages": len(s.Upgrade) + len(s.Install)}, nil
}

const maxLogRead = 64 << 10

func (h *helperServer) aptUpgradeStatus(req helperRequest, logf func(string)) (any, error) {
	running := upgradeRunning()
	state := "idle"
	code := -1
	if b, err := os.ReadFile(aptExit()); err == nil {
		code, _ = strconv.Atoi(strings.TrimSpace(string(b)))
		state = "done"
		if code != 0 {
			state = "failed"
		}
	}
	if running {
		state = "running"
	}
	text, next := "", req.Offset
	if f, err := os.Open(aptLog()); err == nil {
		defer f.Close()
		if st, _ := f.Stat(); st != nil && st.Size() < next {
			next = 0 // a new run started
		}
		if _, err := f.Seek(next, io.SeekStart); err == nil {
			b, _ := io.ReadAll(io.LimitReader(f, maxLogRead))
			text, next = string(b), next+int64(len(b))
		}
	} else if state == "idle" {
		next = 0
	}
	return map[string]any{"state": state, "exit_code": code, "log": text, "offset": next, "reboot_required": rebootRequired()}, nil
}

func upgradeRunning() bool {
	out, _ := exec.Command("systemctl", "is-active", aptUnit).Output()
	s := strings.TrimSpace(string(out))
	return s == "active" || s == "activating"
}

// rebootRequired: Debian/Ubuntu's flag file, or an installed kernel newer than the running one.
func rebootRequired() bool {
	if _, err := os.Stat("/var/run/reboot-required"); err == nil {
		return true
	}
	out, err := exec.Command("uname", "-r").Output()
	if err != nil {
		return false
	}
	running := strings.TrimSpace(string(out))
	kernels, _ := filepath.Glob("/boot/vmlinuz-*")
	if len(kernels) == 0 {
		return false
	}
	for _, k := range kernels {
		v := strings.TrimPrefix(filepath.Base(k), "vmlinuz-")
		if v != running && exec.Command("dpkg", "--compare-versions", v, "gt", running).Run() == nil {
			return true
		}
	}
	return false
}

func autoUpdatesState() map[string]any {
	_, own := os.Stat(autoDropIn)
	out, _ := exec.Command("apt-config", "dump", "APT::Periodic::Unattended-Upgrade").Output()
	on := strings.Contains(string(out), `"1"`)
	_, installed := os.Stat("/usr/bin/unattended-upgrade")
	cfg := defaultAutoCfg()
	readJSONFile(autoCfgPath, &cfg)
	return map[string]any{"enabled": on, "box": own == nil, "installed": installed == nil, "config": cfg, "last": lastAutoRun()}
}

// autoUpdatesSet turns Box's own drop-in on or off. The server's own unattended-upgrades setup
// (50unattended-upgrades, 20auto-upgrades) is never touched; Debian's default only takes security.
func (h *helperServer) autoUpdatesSet(ctx context.Context, req helperRequest, logf func(string)) (any, error) {
	if !hasAptGet() {
		return nil, errNoApt
	}
	switch req.Op {
	case "on":
		if _, err := os.Stat("/usr/bin/unattended-upgrade"); err != nil {
			logf("Installing unattended-upgrades")
			if err := aptInstall(ctx, "unattended-upgrades", logf); err != nil {
				return nil, err
			}
		}
		cfg := defaultAutoCfg()
		if req.AutoCfg != nil {
			cfg = *req.AutoCfg
		}
		if err := cfg.validate(); err != nil {
			return nil, err
		}
		if err := writeFileAtomic(autoDropIn, []byte(autoUpdatesDropIn(cfg)), 0o644); err != nil {
			return nil, err
		}
		os.MkdirAll(filepath.Dir(autoTimerDropIn), 0o755)
		if err := writeFileAtomic(autoTimerDropIn, []byte(autoTimer(cfg)), 0o644); err != nil {
			return nil, err
		}
		writeJSONFile(autoCfgPath, cfg)
		exec.CommandContext(ctx, "systemctl", "daemon-reload").Run()
		exec.CommandContext(ctx, "systemctl", "restart", "apt-daily-upgrade.timer").Run()
	case "off":
		if err := os.Remove(autoDropIn); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if os.Remove(autoTimerDropIn) == nil {
			exec.CommandContext(ctx, "systemctl", "daemon-reload").Run()
			exec.CommandContext(ctx, "systemctl", "restart", "apt-daily-upgrade.timer").Run()
		}
	default:
		return nil, uerr("Choose on or off.")
	}
	return autoUpdatesState(), nil
}

// systemReboot restarts the server a few seconds later, so the answer still reaches the browser.
func (h *helperServer) systemReboot(req helperRequest, logf func(string)) (any, error) {
	if req.Confirm != "REBOOT" {
		return nil, uerr("Type REBOOT to restart the server.")
	}
	if upgradeRunning() {
		return nil, uerr("An update is still running. Restart when it has finished.")
	}
	if out, err := exec.Command("systemd-run", "--on-active=3", "--quiet", "--unit=nexovirt-box-reboot", "systemctl", "reboot").CombinedOutput(); err != nil {
		return nil, uerr("The restart could not be scheduled: %s", strings.TrimSpace(string(out)))
	}
	return map[string]bool{"ok": true}, nil
}

// installable is the fixed list of packages Box may install on request.
var installable = map[string]string{
	"smart": "smartmontools", "samba": "samba", "nfs": "nfs-kernel-server", "unattended": "unattended-upgrades",
	"avahi": "avahi-daemon avahi-utils",
}

func (h *helperServer) pkgInstall(ctx context.Context, req helperRequest, logf func(string)) (any, error) {
	pkg, ok := installable[req.Op]
	if !ok {
		return nil, uerr("Box cannot install this.")
	}
	if !hasAptGet() {
		return nil, errNoApt
	}
	if upgradeRunning() {
		return nil, uerr("A system update is running. Install it when the update has finished.")
	}
	return map[string]bool{"ok": true}, aptInstall(ctx, pkg, logf)
}

func aptInstall(ctx context.Context, pkg string, logf func(string)) error {
	install := func() error {
		args := []string{"apt-get", "install", "-y", "--no-install-recommends", "-o", "Dpkg::Options::=--force-confdef",
			"-o", "Dpkg::Options::=--force-confold", "--"}
		return streamCmd(aptCmd(ctx, append(args, strings.Fields(pkg)...)...), logf) // fixed names from installable
	}
	if install() == nil {
		return nil
	}
	logf("Refreshing the package lists and trying again")
	if streamCmd(aptCmd(ctx, "apt-get", "update"), logf) == nil && install() == nil {
		return nil
	}
	return uerr("%s could not be installed. The log above shows why.", pkg)
}

// streamCmd runs cmd and sends every output line to logf.
func streamCmd(cmd *exec.Cmd, logf func(string)) error {
	pr, pw := io.Pipe()
	cmd.Stdout, cmd.Stderr = pw, pw
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			if l := strings.TrimSpace(sc.Text()); l != "" {
				logf(l)
			}
		}
		io.Copy(io.Discard, pr)
		close(done)
	}()
	err := cmd.Wait()
	pw.Close()
	<-done
	return err
}

func lastLines(s string, n int) string {
	ls := strings.Split(strings.TrimSpace(s), "\n")
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return strings.Join(ls, "\n")
}

func writeFileAtomic(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".nxbox-tmp"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil { // umask 077 in the helper
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}

// sortedKeys is used by the share renderers for stable output.
func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
