package box

// Security check: a score page with findings and, where a safe fix exists, a fix button.
// The helper reads (security_scan): sshd's effective settings (sshd -T), whether any SSH key is
// set up, the firewall's incoming policy, pending OS updates and automatic updates, and which
// apps publish ports on every address. The web side adds the signed-in user's two-step sign-in.
// The only fixes that change the server are SSH settings (ssh_harden, own drop-in only, refused
// without an SSH key, checked with sshd -t, password proof) and automatic updates (existing
// action); the rest are links to the right place.

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

var (
	sshDropIn  = "/etc/ssh/sshd_config.d/10-nexovirt-box.conf"
	sshdConfig = "/etc/ssh/sshd_config"
)

type securityScan struct {
	SSH struct {
		Installed    bool   `json:"installed"`
		PasswordAuth bool   `json:"password_auth"`
		RootLogin    string `json:"root_login"` // yes, no, prohibit-password, ...
		KeysPresent  bool   `json:"keys_present"`
		DropInOK     bool   `json:"drop_in_ok"` // sshd_config includes sshd_config.d
	} `json:"ssh"`
	Firewall struct {
		Backend string `json:"backend"`
		In      string `json:"in"`
	} `json:"firewall"`
	Updates struct {
		Pending int  `json:"pending"`
		Auto    bool `json:"auto"`
		Apt     bool `json:"apt"`
	} `json:"updates"`
	Exposed []string `json:"exposed"` // apps with a port on every address
}

// parseSSHD reads `sshd -T` (lowercase "key value" lines).
func parseSSHD(out string) (passwordAuth bool, rootLogin string) {
	for _, l := range strings.Split(out, "\n") {
		k, v, _ := strings.Cut(strings.TrimSpace(l), " ")
		switch k {
		case "passwordauthentication":
			passwordAuth = v == "yes"
		case "permitrootlogin":
			rootLogin = v
		}
	}
	return passwordAuth, rootLogin
}

func sshKeysPresent() bool {
	homes, _ := filepath.Glob("/home/*")
	for _, d := range append([]string{"/root"}, homes...) {
		if st, err := os.Stat(filepath.Join(d, ".ssh", "authorized_keys")); err == nil && st.Size() > 0 {
			return true
		}
	}
	return false
}

func sshIncludesDropIns() bool {
	b, err := os.ReadFile(sshdConfig)
	if err != nil {
		return false
	}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && strings.EqualFold(f[0], "Include") && strings.Contains(f[1], "sshd_config.d") {
			return true
		}
	}
	return false
}

// exposedApps lists Box apps whose containers publish a port on 0.0.0.0 or [::].
func exposedApps(psOut string) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(psOut), "\n") {
		proj, ports, ok := strings.Cut(l, "|")
		app, mine := strings.CutPrefix(proj, "nxbox-")
		if !ok || !mine || app == "proxy" || seen[app] {
			continue
		}
		if strings.Contains(ports, "0.0.0.0:") || strings.Contains(ports, ":::") {
			seen[app] = true
			out = append(out, app)
		}
	}
	return out
}

func (h *helperServer) securityScan(req helperRequest, _ func(string)) (any, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var s securityScan
	if _, err := exec.LookPath("sshd"); err == nil {
		s.SSH.Installed = true
		out, _ := exec.CommandContext(ctx, "sshd", "-T").Output()
		s.SSH.PasswordAuth, s.SSH.RootLogin = parseSSHD(string(out))
		s.SSH.KeysPresent, s.SSH.DropInOK = sshKeysPresent(), sshIncludesDropIns()
	}
	s.Firewall.Backend = firewallBackend()
	s.Firewall.In = defaultPolicies(s.Firewall.Backend).In
	if hasAptGet() {
		s.Updates.Apt = true
		out, _ := aptCmd(ctx, "apt", "list", "--upgradable").Output()
		s.Updates.Pending = len(parseUpgradable(string(out)))
		s.Updates.Auto, _ = autoUpdatesState()["enabled"].(bool)
	}
	s.Exposed = []string{}
	if needDocker() == nil {
		if out, err := exec.CommandContext(ctx, "docker", "ps", "--format", `{{.Label "com.docker.compose.project"}}|{{.Ports}}`).Output(); err == nil {
			s.Exposed = exposedApps(string(out))
		}
	}
	return s, nil
}

var errProofSSH = userError{"Enter your password to change the SSH settings."}

// sshHarden turns off SSH password sign-in (op "password") or root sign-in with a password (op
// "root") in Box's own drop-in, after checking that an SSH key exists and with sshd -t.
func (h *helperServer) sshHarden(req helperRequest, logf func(string)) (any, error) {
	if !h.consoleAllowed(req) {
		return nil, errProofSSH
	}
	if req.Op != "password" && req.Op != "root" {
		return nil, uerr("This action is not allowed.")
	}
	if _, err := exec.LookPath("sshd"); err != nil {
		return nil, uerr("No SSH server is installed.")
	}
	if !sshIncludesDropIns() {
		return nil, uerr("This server's SSH configuration does not read sshd_config.d, so Box cannot change it safely. Change it by hand.")
	}
	if req.Op == "password" && !sshKeysPresent() {
		return nil, uerr("No SSH key is set up for any user. Add your key first, or you could lock yourself out of SSH (the Box terminal keeps working).")
	}
	prev, prevErr := os.ReadFile(sshDropIn)
	want := map[string]string{}
	for _, l := range strings.Split(string(prev), "\n") {
		if f := strings.Fields(l); len(f) == 2 && !strings.HasPrefix(f[0], "#") {
			want[f[0]] = f[1]
		}
	}
	if req.Op == "password" {
		want["PasswordAuthentication"] = "no"
		want["KbdInteractiveAuthentication"] = "no"
	} else {
		want["PermitRootLogin"] = "prohibit-password"
	}
	body := "# Written by NexoVirt Box (Security check). Delete this file to undo.\n"
	for _, k := range []string{"PasswordAuthentication", "KbdInteractiveAuthentication", "PermitRootLogin"} {
		if v, ok := want[k]; ok {
			body += k + " " + v + "\n"
		}
	}
	os.MkdirAll(filepath.Dir(sshDropIn), 0o755)
	if err := writeFileAtomic(sshDropIn, []byte(body), 0o644); err != nil {
		return nil, err
	}
	restore := func() {
		if prevErr == nil {
			writeFileAtomic(sshDropIn, prev, 0o644)
		} else {
			os.Remove(sshDropIn)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "sshd", "-t").CombinedOutput(); err != nil {
		restore()
		return nil, uerr("The SSH configuration did not pass its check, nothing was changed: %s", strings.TrimSpace(lastLine(string(out))))
	}
	if exec.CommandContext(ctx, "systemctl", "reload", "ssh").Run() != nil && exec.CommandContext(ctx, "systemctl", "reload", "sshd").Run() != nil {
		restore()
		return nil, uerr("The SSH server could not reload its settings, nothing was changed.")
	}
	return map[string]bool{"ok": true}, nil
}

// ---- web ----

type securityFinding struct {
	Key    string `json:"key"`
	Level  string `json:"level"` // high, medium, low
	Title  string `json:"title"`
	Text   string `json:"text"`
	Fix    string `json:"fix,omitempty"`  // action key the UI runs (ssh_password, ssh_root, auto_updates)
	Link   string `json:"link,omitempty"` // a place in Box to fix it by hand
	Action string `json:"action,omitempty"`
}

var levelCost = map[string]int{"high": 25, "medium": 15, "low": 5}

// securityFindings is pure: findings and a score from the scan and the user's 2FA.
func securityFindings(s securityScan, twoFactor bool) ([]securityFinding, int) {
	var out []securityFinding
	if s.SSH.Installed && s.SSH.PasswordAuth {
		f := securityFinding{Key: "ssh_password", Level: "high", Title: "SSH accepts passwords",
			Text: "Bots try passwords on SSH all day. Keys are much safer."}
		if s.SSH.KeysPresent && s.SSH.DropInOK {
			f.Fix, f.Action = "ssh_password", "Turn off password sign-in"
		} else {
			f.Text += " Add an SSH key first, then Box can turn passwords off."
		}
		out = append(out, f)
	}
	if s.SSH.Installed && s.SSH.RootLogin == "yes" {
		f := securityFinding{Key: "ssh_root", Level: "medium", Title: "root can sign in to SSH with a password",
			Text: "root is the first account attackers try."}
		if s.SSH.DropInOK {
			f.Fix, f.Action = "ssh_root", "Allow root with a key only"
		}
		out = append(out, f)
	}
	if s.Firewall.Backend == "" || s.Firewall.Backend == "none" || s.Firewall.In == "allow" {
		out = append(out, securityFinding{Key: "firewall", Level: "high", Title: "No firewall blocks incoming traffic",
			Text: "Every port a program opens is reachable. Turn on the firewall and allow only what you need.", Link: "/settings/firewall", Action: "Open Firewall"})
	}
	if len(s.Exposed) > 0 && (s.Firewall.In == "allow" || s.Firewall.Backend == "" || s.Firewall.Backend == "none") {
		out = append(out, securityFinding{Key: "exposed", Level: "medium", Title: "Apps are open on every address",
			Text: "Reachable from the internet if this server is: " + strings.Join(s.Exposed, ", ") + ". Use a domain with the Box sign-in or the firewall.", Link: "/apps/installed", Action: "Open Apps"})
	}
	if s.Updates.Pending > 0 {
		lvl := "low"
		if s.Updates.Pending >= 20 {
			lvl = "medium"
		}
		out = append(out, securityFinding{Key: "updates", Level: lvl, Title: "System updates are waiting",
			Text: "Pending package updates often close security holes.", Link: "/settings/system-updates", Action: "Open System updates"})
	}
	if s.Updates.Apt && !s.Updates.Auto {
		out = append(out, securityFinding{Key: "auto_updates", Level: "low", Title: "Automatic security updates are off",
			Text: "Security fixes then wait until you install them by hand.", Fix: "auto_updates", Action: "Turn on"})
	}
	if !twoFactor {
		out = append(out, securityFinding{Key: "2fa", Level: "medium", Title: "Two-step sign-in is off for you",
			Text: "With a code app or a passkey a stolen password alone does not open Box.", Link: "/settings/security", Action: "Set it up"})
	}
	score := 100
	for _, f := range out {
		score -= levelCost[f.Level]
	}
	return out, max(score, 0)
}

func (a *App) securityCheck(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	raw, err := a.helper.call(ctx, helperRequest{Action: "security_scan"}, nil)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, friendly(err))
		return
	}
	var s securityScan
	json.Unmarshal(raw, &s)
	uid, _ := a.currentUser(r)
	keys, _ := a.store.passkeys(uid)
	findings, score := securityFindings(s, a.store.totpSecret(uid) != "" || len(keys) > 0)
	if findings == nil {
		findings = []securityFinding{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"score": score, "findings": findings, "checked_at": time.Now().Unix()})
}

// securityFix runs the SSH fixes (password proof checked by the helper).
func (a *App) securityFix(w http.ResponseWriter, r *http.Request) {
	var in struct{ Fix string }
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	op := map[string]string{"ssh_password": "password", "ssh_root": "root"}[in.Fix]
	if op == "" {
		writeError(w, http.StatusBadRequest, "This fix is not available.")
		return
	}
	if !a.needProof(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	if _, err := a.helper.call(ctx, a.proofReq(r, helperRequest{Action: "ssh_harden", Op: op}), nil); err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	a.audit(r, "security.fix", in.Fix)
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
