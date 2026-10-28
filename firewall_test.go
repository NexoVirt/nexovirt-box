package main

import (
	"strings"
	"testing"
)

func TestFirewallRulesValidated(t *testing.T) {
	for _, bad := range []fwRule{
		{Action: "drop", Proto: "tcp", Port: "80"},
		{Action: "allow", Proto: "icmp", Port: "80"},
		{Action: "allow", Proto: "tcp", Port: "0"},
		{Action: "allow", Proto: "tcp", Port: "9000-8000"},
		{Action: "allow", Proto: "tcp", Port: "80; flush ruleset"},
		{Action: "allow", Proto: "tcp", Port: "80", Source: "1.2.3.4; drop"},
		{Action: "allow", Proto: "tcp", Port: "80", Comment: `x" ; flush`},
	} {
		if _, err := validateRules([]fwRule{bad}); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	rules, err := validateRules([]fwRule{{Action: "ALLOW", Proto: "tcp", Port: "8080", Source: "10.0.0.0/8"}, {Action: "deny", Proto: "udp", Port: "53"}})
	if err != nil || rules[0].Action != "allow" || len(rules[0].ID) != 8 {
		t.Fatalf("%v %v", rules, err)
	}
	s := nftScript(rules)
	for _, want := range []string{"table inet nexovirt_box", "ip saddr 10.0.0.0/8 tcp dport 8080 accept", "udp dport 53 drop", "ct original proto-dst 53 drop", "delete table inet nexovirt_box"} {
		if !strings.Contains(s, want) {
			t.Fatalf("nft script misses %q:\n%s", want, s)
		}
	}
}

func TestBlocksBoxPort(t *testing.T) {
	if !blocksPort([]fwRule{{Action: "deny", Proto: "tcp", Port: "8000-9000"}}, 8443) {
		t.Fatal("range block not seen")
	}
	if blocksPort([]fwRule{{Action: "allow", Proto: "tcp", Port: "8443"}, {Action: "deny", Proto: "udp", Port: "8443"}}, 8443) {
		t.Fatal("allow or udp counted as block")
	}
}

func TestDefaultPolicyParsing(t *testing.T) {
	rs := "table inet filter {\n\tchain input {\n\t\ttype filter hook input priority filter; policy drop;\n\t}\n\tchain output {\n\t\ttype filter hook output priority filter; policy accept;\n\t}\n}\ntable inet nexovirt_box {\n\tchain out {\n\t\ttype filter hook output priority 0; policy drop;\n\t}\n}\n"
	if p := parseNftPolicies(rs); p.In != "deny" || p.Out != "allow" {
		t.Fatalf("nft: %+v", p)
	}
	if p := parseNftPolicies(""); p.In != "allow" || p.Out != "allow" {
		t.Fatalf("empty ruleset: %+v", p)
	}
	m := ufwDefaultRe.FindStringSubmatch("Status: active\nDefault: deny (incoming), allow (outgoing), disabled (routed)\n")
	if m == nil || policyWord(m[1]) != "deny" || policyWord(m[2]) != "allow" {
		t.Fatalf("ufw: %v", m)
	}
}
