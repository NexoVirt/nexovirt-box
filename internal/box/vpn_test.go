package box

import (
	"strings"
	"testing"
)

func TestVPNConfigs(t *testing.T) {
	priv, pub, err := wgKeyPair()
	if err != nil || !wgKeyRe.MatchString(priv) || !wgKeyRe.MatchString(pub) {
		t.Fatalf("keys %q %q %v", priv, pub, err)
	}
	if p, _ := wgPublic(priv); p != pub {
		t.Fatal("public key does not match")
	}
	if s := pickSubnet([]string{"10.88.38.5/24", "192.168.1.10/24"}); s == "10.88.38.0/24" {
		t.Fatal("overlapping subnet picked")
	}
	if s := pickSubnet([]string{"10.0.0.2/8"}); !strings.HasPrefix(s, "172.30.") {
		t.Fatalf("subnet inside a 10/8 LAN: %s", s)
	}
	s := vpnState{Port: 51820, Endpoint: "box.example.com", Subnet: "10.88.38.0/24", LAN: "192.168.1.0/24", ServerPub: pub,
		Clients: []vpnClient{{Name: "Phone", Pub: pub, IP: "10.88.38.2"}}}
	conf := serverConf(s, priv)
	for _, want := range []string{"Address = 10.88.38.1/24", "ListenPort = 51820", "AllowedIPs = 10.88.38.2/32", "-d 192.168.1.0/24 -j ACCEPT"} {
		if !strings.Contains(conf, want) {
			t.Fatalf("server conf lacks %q:\n%s", want, conf)
		}
	}
	nft := vpnNft(s)
	if !strings.Contains(nft, `iifname "nxbox0" ip daddr 192.168.1.0/24 accept`) || !strings.Contains(nft, `iifname "nxbox0" drop`) {
		t.Fatalf("nft:\n%s", nft)
	}
	cc := clientConf(s, s.Clients[0], priv)
	if !strings.Contains(cc, "AllowedIPs = 10.88.38.0/24, 192.168.1.0/24") || !strings.Contains(cc, "Endpoint = box.example.com:51820") || strings.Contains(cc, "0.0.0.0/0") {
		t.Fatalf("client conf (split tunnel only):\n%s", cc)
	}
	for _, bad := range []string{"", "box", "evil.com\nPostUp = rm -rf /", "a b.com"} {
		if checkEndpoint(bad) == nil {
			t.Errorf("endpoint %q accepted", bad)
		}
	}
	if checkEndpoint("203.0.113.7") != nil || checkEndpoint("box.example.com") != nil {
		t.Fatal("good endpoint refused")
	}
}

func TestVPNNeedsProof(t *testing.T) {
	h := &helperServer{appsDir: t.TempDir(), webState: t.TempDir(), proofs: newProofKeeper()}
	if _, err := h.vpnSetup(helperRequest{Op: "on", Domain: "box.example.com"}, nil); err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("vpn on without proof: %v", err)
	}
	if _, err := h.vpnClientAdd(helperRequest{Name: "x"}, nil); err == nil || !strings.Contains(err.Error(), "password") {
		t.Fatalf("client without proof: %v", err)
	}
}
