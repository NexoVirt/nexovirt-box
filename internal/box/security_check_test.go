package box

import (
	"strings"
	"testing"
)

func TestSecurityParsers(t *testing.T) {
	pw, root := parseSSHD("port 22\npermitrootlogin yes\npasswordauthentication yes\nx y\n")
	if !pw || root != "yes" {
		t.Fatalf("%v %q", pw, root)
	}
	if pw, _ := parseSSHD("passwordauthentication no\n"); pw {
		t.Fatal("no read as yes")
	}
	ps := "nxbox-memos|0.0.0.0:5230->5230/tcp, :::5230->5230/tcp\nnxbox-wiki|127.0.0.1:8080->80/tcp\nnxbox-proxy|\nother|0.0.0.0:22->22/tcp\nnxbox-memos|0.0.0.0:1->1/tcp"
	if got := strings.Join(exposedApps(ps), ","); got != "memos" {
		t.Fatalf("exposed %q", got)
	}
}

func TestSecurityFindingsAndScore(t *testing.T) {
	var s securityScan
	s.SSH.Installed, s.SSH.PasswordAuth, s.SSH.RootLogin, s.SSH.DropInOK = true, true, "yes", true
	s.Firewall.Backend, s.Firewall.In = "nftables", "allow"
	s.Updates.Apt, s.Updates.Pending = true, 3
	s.Exposed = []string{"memos"}
	fs, score := securityFindings(s, false)
	keys := map[string]securityFinding{}
	for _, f := range fs {
		keys[f.Key] = f
	}
	for _, k := range []string{"ssh_password", "ssh_root", "firewall", "exposed", "updates", "auto_updates", "2fa"} {
		if _, ok := keys[k]; !ok {
			t.Errorf("missing finding %s", k)
		}
	}
	if keys["ssh_password"].Fix != "" {
		t.Fatal("password sign-in can be turned off without any SSH key")
	}
	if keys["ssh_root"].Fix != "ssh_root" || keys["auto_updates"].Fix != "auto_updates" {
		t.Fatal("safe fixes missing")
	}
	if score != 0 {
		t.Fatalf("score %d", score)
	}
	s.SSH.KeysPresent = true
	if fs, _ := securityFindings(s, true); fs[0].Fix != "ssh_password" {
		t.Fatal("with a key the password fix must be offered")
	}
	var good securityScan
	good.SSH.Installed, good.SSH.RootLogin = true, "prohibit-password"
	good.Firewall.Backend, good.Firewall.In = "ufw", "deny"
	good.Updates.Apt, good.Updates.Auto = true, true
	good.Exposed = []string{"memos"} // behind a firewall that denies by default
	if fs, score := securityFindings(good, true); len(fs) != 0 || score != 100 {
		t.Fatalf("clean server: %v %d", fs, score)
	}
}

// SSH changes need a password proof checked by the helper itself.
func TestSSHHardenNeedsProof(t *testing.T) {
	h := &helperServer{appsDir: t.TempDir(), webState: t.TempDir(), proofs: newProofKeeper()}
	if _, err := h.sshHarden(helperRequest{Op: "password"}, func(string) {}); err != errProofSSH {
		t.Fatalf("no proof: %v", err)
	}
}
