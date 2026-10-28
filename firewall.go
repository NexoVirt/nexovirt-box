package main

// Firewall: Box manages ONLY its own rules (its own nftables table, ufw rules tagged with a
// comment, or the firewalld rich rules it added itself) and shows every other rule read only.
// Every change has a 60 second confirm window: the browser must confirm it with a fresh request
// over the new rules, otherwise the helper puts the previous rules back (lockout protection).
// Blocking the Box port needs a typed confirmation.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

var firewallDir = "/etc/nexovirt-box"

const confirmWindow = 60 * time.Second

type fwRule struct {
	ID      string `json:"id"`
	Action  string `json:"action"` // allow | deny
	Proto   string `json:"proto"`  // tcp | udp
	Port    string `json:"port"`   // "8080" or "8000-8100"
	Source  string `json:"source"` // "" = anywhere, else IP or CIDR
	Comment string `json:"comment"`
}

type fwState struct {
	Rules   []fwRule `json:"rules"`
	Applied []string `json:"applied,omitempty"` // firewalld: rich rules Box added
}

type fwPending struct {
	ID       string   `json:"id"`
	Previous []fwRule `json:"previous"`
	Deadline int64    `json:"deadline"`
}

var fwMu sync.Mutex

func fwPath(name string) string { return filepath.Join(firewallDir, name) }

func readJSONFile(path string, v any) bool {
	b, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(b, v) == nil
}

func writeJSONFile(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// firewallBackend picks the tool the server already uses.
func firewallBackend() string {
	if out, err := exec.Command("ufw", "status").Output(); err == nil && strings.Contains(string(out), "Status: active") {
		return "ufw"
	}
	if out, err := exec.Command("firewall-cmd", "--state").Output(); err == nil && strings.TrimSpace(string(out)) == "running" {
		return "firewalld"
	}
	if _, err := exec.LookPath("nft"); err == nil {
		return "nftables"
	}
	return ""
}

var (
	portRe    = regexp.MustCompile(`^\d{1,5}(-\d{1,5})?$`)
	commentRe = regexp.MustCompile(`^[A-Za-z0-9 ._,:()/+-]{0,60}$`)
)

func validateRules(rules []fwRule) ([]fwRule, error) {
	if len(rules) > 200 {
		return nil, uerr("Box manages at most 200 rules.")
	}
	out := make([]fwRule, 0, len(rules))
	for i, r := range rules {
		r.Action, r.Proto = strings.ToLower(r.Action), strings.ToLower(r.Proto)
		r.Port, r.Source, r.Comment = strings.TrimSpace(r.Port), strings.TrimSpace(r.Source), strings.TrimSpace(r.Comment)
		if r.Action != "allow" && r.Action != "deny" {
			return nil, uerr("Rule %d: choose allow or block.", i+1)
		}
		if r.Proto != "tcp" && r.Proto != "udp" {
			return nil, uerr("Rule %d: choose TCP or UDP.", i+1)
		}
		lo, hi, ok := portRange(r.Port)
		if !ok {
			return nil, uerr("Rule %d: enter a port (1 to 65535) or a range like 8000-8100.", i+1)
		}
		if lo == hi {
			r.Port = strconv.Itoa(lo)
		}
		if r.Source != "" {
			if ip := net.ParseIP(r.Source); ip != nil {
				r.Source = ip.String()
			} else if _, n, err := net.ParseCIDR(r.Source); err == nil {
				r.Source = n.String()
			} else {
				return nil, uerr("Rule %d: the source must be an IP address or a network like 192.168.1.0/24.", i+1)
			}
		}
		if !commentRe.MatchString(r.Comment) {
			return nil, uerr("Rule %d: the note may only use letters, digits and simple punctuation (60 at most).", i+1)
		}
		if r.ID == "" || !regexp.MustCompile(`^[a-f0-9]{8}$`).MatchString(r.ID) {
			r.ID = randomToken()[:8]
		}
		out = append(out, r)
	}
	return out, nil
}

func portRange(p string) (int, int, bool) {
	if !portRe.MatchString(p) {
		return 0, 0, false
	}
	a, b, isRange := strings.Cut(p, "-")
	lo, _ := strconv.Atoi(a)
	hi := lo
	if isRange {
		hi, _ = strconv.Atoi(b)
	}
	return lo, hi, lo >= 1 && hi <= 65535 && lo <= hi
}

// blocksPort reports whether a deny rule hits port for everyone (or a wide range).
func blocksPort(rules []fwRule, port int) bool {
	for _, r := range rules {
		lo, hi, _ := portRange(r.Port)
		if r.Action == "deny" && r.Proto == "tcp" && lo <= port && port <= hi {
			return true
		}
	}
	return false
}

// ---- backends ----

func nftFamily(src string) string {
	if strings.Contains(src, ":") {
		return "ip6"
	}
	return "ip"
}

func nftScript(rules []fwRule) string {
	var in, fwd strings.Builder
	for _, r := range rules {
		match := ""
		if r.Source != "" {
			match = nftFamily(r.Source) + " saddr " + r.Source + " "
		}
		verdict := "accept"
		if r.Action == "deny" {
			verdict = "drop"
		}
		fmt.Fprintf(&in, "\t\t%s%s dport %s %s comment \"box:%s\"\n", match, r.Proto, r.Port, verdict, r.ID)
		if r.Action == "deny" {
			// Docker forwards published ports (DNAT), so they never pass the input hook.
			fmt.Fprintf(&fwd, "\t\t%smeta l4proto %s ct original proto-dst %s drop comment \"box:%s\"\n", match, r.Proto, r.Port, r.ID)
		}
	}
	return "add table inet nexovirt_box\ndelete table inet nexovirt_box\ntable inet nexovirt_box {\n" +
		"\tchain input {\n\t\ttype filter hook input priority -5; policy accept;\n" + in.String() + "\t}\n" +
		"\tchain forward {\n\t\ttype filter hook forward priority -5; policy accept;\n" + fwd.String() + "\t}\n}\n"
}

func runInput(ctx context.Context, input string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(input)
	if out, err := cmd.CombinedOutput(); err != nil {
		return uerr("The firewall refused the change: %s", strings.TrimSpace(lastLine(string(out))))
	}
	return nil
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		return string(out), uerr("The firewall refused the change: %s", strings.TrimSpace(lastLine(string(out))))
	}
	return string(out), nil
}

var ufwNumRe = regexp.MustCompile(`^\[\s*(\d+)\]`)

func richRule(r fwRule) string {
	fam := "ipv4"
	src := ""
	if r.Source != "" {
		if strings.Contains(r.Source, ":") {
			fam = "ipv6"
		}
		src = fmt.Sprintf(` source address="%s"`, r.Source)
	}
	verdict := "accept"
	if r.Action == "deny" {
		verdict = "drop"
	}
	return fmt.Sprintf(`rule family="%s"%s port port="%s" protocol="%s" %s`, fam, src, r.Port, r.Proto, verdict)
}

// applyRules replaces Box's own rules with rules on the detected backend.
func applyRules(rules []fwRule) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var st fwState
	readJSONFile(fwPath("firewall.json"), &st)
	switch firewallBackend() {
	case "nftables":
		if err := runInput(ctx, nftScript(rules), "nft", "-f", "-"); err != nil {
			return err
		}
	case "ufw":
		out, err := run(ctx, "ufw", "status", "numbered")
		if err != nil {
			return err
		}
		var nums []int
		for _, l := range strings.Split(out, "\n") {
			if m := ufwNumRe.FindStringSubmatch(l); m != nil && strings.Contains(l, "# nexovirt-box:") {
				n, _ := strconv.Atoi(m[1])
				nums = append(nums, n)
			}
		}
		sort.Sort(sort.Reverse(sort.IntSlice(nums))) // delete from the bottom so numbers stay valid
		for _, n := range nums {
			if _, err := run(ctx, "ufw", "--force", "delete", strconv.Itoa(n)); err != nil {
				return err
			}
		}
		for _, r := range rules {
			verb := "allow"
			if r.Action == "deny" {
				verb = "deny"
			}
			args := []string{verb}
			if r.Source != "" {
				args = append(args, "from", r.Source, "to", "any")
			} else {
				args = append(args, "from", "any", "to", "any")
			}
			args = append(args, "port", strings.ReplaceAll(r.Port, "-", ":"), "proto", r.Proto, "comment", "nexovirt-box:"+r.ID)
			if _, err := run(ctx, "ufw", args...); err != nil {
				return err
			}
		}
	case "firewalld":
		for _, rr := range st.Applied {
			run(ctx, "firewall-cmd", "--permanent", "--remove-rich-rule="+rr)
		}
		st.Applied = nil
		for _, r := range rules {
			rr := richRule(r)
			if _, err := run(ctx, "firewall-cmd", "--permanent", "--add-rich-rule="+rr); err != nil {
				return err
			}
			st.Applied = append(st.Applied, rr)
		}
		if _, err := run(ctx, "firewall-cmd", "--reload"); err != nil {
			return err
		}
	default:
		return uerr("No firewall tool was found on this server. Install nftables (apt install nftables) and try again.")
	}
	st.Rules = rules
	return writeJSONFile(fwPath("firewall.json"), st)
}

// foreignRules returns the rules Box does not own, as the tool prints them (read only).
func foreignRules(backend string) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var out []byte
	switch backend {
	case "nftables":
		out, _ = exec.CommandContext(ctx, "nft", "list", "ruleset").Output()
		var keep []string
		skip := false
		for _, l := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(l, "table inet nexovirt_box") {
				skip = true
			}
			if !skip && strings.TrimSpace(l) != "" {
				keep = append(keep, l)
			}
			if skip && l == "}" {
				skip = false
			}
		}
		return keep
	case "ufw":
		out, _ = exec.CommandContext(ctx, "ufw", "status", "numbered").Output()
		var keep []string
		for _, l := range strings.Split(string(out), "\n") {
			if ufwNumRe.MatchString(l) && !strings.Contains(l, "# nexovirt-box:") {
				keep = append(keep, strings.TrimSpace(l))
			}
		}
		return keep
	case "firewalld":
		out, _ = exec.CommandContext(ctx, "firewall-cmd", "--list-all").Output()
	}
	return strings.Split(strings.TrimSpace(string(out)), "\n")
}

// ---- helper actions ----

func (h *helperServer) firewallStatus(req helperRequest, logf func(string)) (any, error) {
	fwMu.Lock()
	defer fwMu.Unlock()
	backend := firewallBackend()
	var st fwState
	readJSONFile(fwPath("firewall.json"), &st)
	if st.Rules == nil {
		st.Rules = []fwRule{}
	}
	res := map[string]any{"backend": backend, "rules": st.Rules, "foreign": foreignRules(backend), "policies": defaultPolicies(backend)}
	var rb struct {
		ID string `json:"id"`
		At int64  `json:"at"`
	}
	if readJSONFile(fwPath("firewall.rollback.json"), &rb) {
		res["last_rollback"] = rb
	}
	var p fwPending
	if readJSONFile(fwPath("firewall.pending.json"), &p) {
		res["pending"] = map[string]any{"id": p.ID, "seconds_left": max(0, p.Deadline-time.Now().Unix())}
	}
	return res, nil
}

// firewallApply applies the new rule set and starts the confirm window.
func (h *helperServer) firewallApply(req helperRequest, logf func(string)) (any, error) {
	fwMu.Lock()
	defer fwMu.Unlock()
	if readJSONFile(fwPath("firewall.pending.json"), &fwPending{}) {
		return nil, uerr("Confirm or wait for the last change first.")
	}
	rules, err := validateRules(req.Rules)
	if err != nil {
		return nil, err
	}
	boxPort := req.HTTPPort
	if boxPort == 0 {
		boxPort = 8443
	}
	if blocksPort(rules, boxPort) && req.Confirm != "BLOCK" {
		return nil, uerr("This change blocks port %d, which NexoVirt Box itself uses. Type BLOCK to confirm.", boxPort)
	}
	var st fwState
	readJSONFile(fwPath("firewall.json"), &st)
	p := fwPending{ID: randomToken()[:12], Previous: st.Rules, Deadline: time.Now().Add(confirmWindow).Unix()}
	if p.Previous == nil {
		p.Previous = []fwRule{}
	}
	if err := writeJSONFile(fwPath("firewall.pending.json"), p); err != nil {
		return nil, err
	}
	if err := applyRules(rules); err != nil {
		os.Remove(fwPath("firewall.pending.json"))
		applyRules(p.Previous)
		return nil, err
	}
	time.AfterFunc(confirmWindow, func() { rollbackIfPending(p.ID) })
	return map[string]any{"change": p.ID, "seconds": int(confirmWindow.Seconds())}, nil
}

func (h *helperServer) firewallConfirm(req helperRequest, logf func(string)) (any, error) {
	fwMu.Lock()
	defer fwMu.Unlock()
	var p fwPending
	if !readJSONFile(fwPath("firewall.pending.json"), &p) || p.ID != req.ID {
		return nil, uerr("This change is no longer waiting for confirmation. It may have been rolled back.")
	}
	os.Remove(fwPath("firewall.pending.json"))
	return map[string]bool{"ok": true}, nil
}

// rollbackIfPending restores the previous rules when the change id is still unconfirmed.
func rollbackIfPending(id string) {
	fwMu.Lock()
	defer fwMu.Unlock()
	var p fwPending
	if !readJSONFile(fwPath("firewall.pending.json"), &p) || (id != "" && p.ID != id) {
		return
	}
	if err := applyRules(p.Previous); err != nil {
		fmt.Fprintf(os.Stderr, "firewall rollback: %v\n", err)
		return
	}
	os.Remove(fwPath("firewall.pending.json"))
	writeJSONFile(fwPath("firewall.rollback.json"), map[string]any{"id": p.ID, "at": time.Now().Unix()}) // for notifications
	fmt.Fprintf(os.Stderr, "firewall: change %s was not confirmed, previous rules restored\n", p.ID)
}

// restoreFirewall runs when the helper starts (also at boot): an unconfirmed change is rolled
// back, and Box's rules are applied again (nftables rules do not survive a reboot on their own).
func restoreFirewall() {
	if readJSONFile(fwPath("firewall.pending.json"), &fwPending{}) {
		rollbackIfPending("")
		return
	}
	var st fwState
	if readJSONFile(fwPath("firewall.json"), &st) && len(st.Rules) > 0 && firewallBackend() == "nftables" {
		fwMu.Lock()
		if err := applyRules(st.Rules); err != nil {
			fmt.Fprintf(os.Stderr, "firewall restore: %v\n", err)
		}
		fwMu.Unlock()
	}
}

// fwPolicies is what happens to traffic no rule matches (shown on the Firewall overview).
type fwPolicies struct {
	In  string `json:"in"`  // allow | deny | unknown
	Out string `json:"out"` // allow | deny | unknown
}

var ufwDefaultRe = regexp.MustCompile(`Default:\s*(\w+)\s*\(incoming\),\s*(\w+)\s*\(outgoing\)`)

func policyWord(s string) string {
	switch strings.ToLower(s) {
	case "accept", "allow", "default":
		return "allow"
	case "drop", "deny", "reject", "%%reject%%":
		return "deny"
	}
	return "unknown"
}

// parseNftPolicies reads the policy of the input/output filter chains outside Box's own table.
// No such chain means the kernel lets the traffic through.
func parseNftPolicies(ruleset string) fwPolicies {
	p := fwPolicies{In: "allow", Out: "allow"}
	ours := false
	for _, l := range strings.Split(ruleset, "\n") {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(l, "table ") {
			ours = strings.HasPrefix(l, "table inet nexovirt_box")
		}
		if ours || !strings.Contains(t, "type filter hook") {
			continue
		}
		pol := "allow"
		if i := strings.Index(t, "policy "); i >= 0 {
			pol = policyWord(strings.Trim(strings.Fields(t[i+7:])[0], ";"))
		}
		if strings.Contains(t, "hook input") && pol == "deny" {
			p.In = "deny"
		}
		if strings.Contains(t, "hook output") && pol == "deny" {
			p.Out = "deny"
		}
	}
	return p
}

func defaultPolicies(backend string) fwPolicies {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	switch backend {
	case "nftables":
		out, err := exec.CommandContext(ctx, "nft", "list", "ruleset").Output()
		if err == nil {
			return parseNftPolicies(string(out))
		}
	case "ufw":
		out, _ := exec.CommandContext(ctx, "ufw", "status", "verbose").Output()
		if m := ufwDefaultRe.FindStringSubmatch(string(out)); m != nil {
			return fwPolicies{In: policyWord(m[1]), Out: policyWord(m[2])}
		}
	case "firewalld":
		zone, _ := exec.CommandContext(ctx, "firewall-cmd", "--get-default-zone").Output()
		target, _ := exec.CommandContext(ctx, "firewall-cmd", "--permanent", "--zone="+strings.TrimSpace(string(zone)), "--get-target").Output()
		in := "deny" // firewalld's "default" target rejects what no service allows
		if strings.EqualFold(strings.TrimSpace(string(target)), "ACCEPT") {
			in = "allow"
		}
		return fwPolicies{In: in, Out: "allow"}
	}
	return fwPolicies{In: "unknown", Out: "unknown"}
}
