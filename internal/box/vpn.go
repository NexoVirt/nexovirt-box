package box

// One-click WireGuard VPN: interface nxbox0 (wg-quick) on the box, clients that reach only the
// box and its local network (split tunnel, no internet through the box), configs with QR codes
// shown once, revoke. Box owns /etc/wireguard/nxbox0.conf (0600, the server key never leaves
// it), /etc/nexovirt-box/vpn.json (public keys only) and /etc/nexovirt-box/vpn.nft (its own
// nftables table: VPN traffic may go to the LAN only, masqueraded so LAN devices can answer).
// A client's private key is made in the helper, handed out once and never stored, and never
// passes through a job (jobs keep their results).

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/curve25519"
)

var (
	wgConf       = "/etc/wireguard/nxbox0.conf"
	vpnStatePath = "/etc/nexovirt-box/vpn.json"
	vpnNftPath   = "/etc/nexovirt-box/vpn.nft"
	vpnSysctl    = "/etc/sysctl.d/90-nexovirt-box-vpn.conf"
	vpnNameRe    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,39}$`)
	endpointRe   = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9.-]{0,252}[A-Za-z0-9])?$`)
	wgKeyRe      = regexp.MustCompile(`^[A-Za-z0-9+/]{43}=$`)
)

type vpnClient struct {
	Name    string `json:"name"`
	Pub     string `json:"pub"`
	IP      string `json:"ip"`
	Created int64  `json:"created"`
}

type vpnState struct {
	Port      int         `json:"port"`
	Endpoint  string      `json:"endpoint"` // host name or address clients connect to
	Subnet    string      `json:"subnet"`   // e.g. 10.88.37.0/24, the box is .1
	LAN       string      `json:"lan"`      // the local network clients may reach
	ServerPub string      `json:"server_pub"`
	Clients   []vpnClient `json:"clients"`
}

func wgKeyPair() (priv, pub string, err error) {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return "", "", err
	}
	k[0] &= 248
	k[31] = (k[31] & 127) | 64
	p, err := curve25519.X25519(k, curve25519.Basepoint)
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(k), base64.StdEncoding.EncodeToString(p), nil
}

func wgPublic(priv string) (string, error) {
	k, err := base64.StdEncoding.DecodeString(priv)
	if err != nil || len(k) != 32 {
		return "", errors.New("bad key")
	}
	p, err := curve25519.X25519(k, curve25519.Basepoint)
	return base64.StdEncoding.EncodeToString(p), err
}

// lanOf is the network of the interface that carries ip (e.g. 192.168.1.0/24).
func lanOf(ip string) string {
	addrs, _ := net.InterfaceAddrs()
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok && n.IP.String() == ip && n.IP.To4() != nil {
			return (&net.IPNet{IP: n.IP.Mask(n.Mask), Mask: n.Mask}).String()
		}
	}
	return ""
}

// pickSubnet chooses a /24 (10.88.N, then 172.30.N, then 192.168.N) that overlaps no local
// network; "" when every candidate is taken.
func pickSubnet(taken []string) string {
	var nets []*net.IPNet
	for _, t := range taken {
		if _, n, err := net.ParseCIDR(t); err == nil {
			nets = append(nets, n)
		}
	}
	var cands []string
	for n := 37; n < 37+250; n++ {
		cands = append(cands, fmt.Sprintf("10.88.%d.0/24", n%250+1))
	}
	for n := 1; n < 250; n++ {
		cands = append(cands, fmt.Sprintf("172.30.%d.0/24", n))
	}
	for n := 200; n < 250; n++ {
		cands = append(cands, fmt.Sprintf("192.168.%d.0/24", n))
	}
	for _, cand := range cands {
		_, c, _ := net.ParseCIDR(cand)
		free := true
		for _, x := range nets {
			if x.Contains(c.IP) || c.Contains(x.IP) {
				free = false
			}
		}
		if free {
			return cand
		}
	}
	return ""
}

func hostIn(subnet string, n int) string {
	ip, _, _ := net.ParseCIDR(subnet)
	v := ip.To4()
	return fmt.Sprintf("%d.%d.%d.%d", v[0], v[1], v[2], n)
}

func readVPN() (vpnState, bool) {
	var s vpnState
	ok := readJSONFile(vpnStatePath, &s)
	return s, ok && s.Subnet != ""
}

// serverConf renders nxbox0.conf from the state and the server's private key.
func serverConf(s vpnState, priv string) string {
	var b strings.Builder
	b.WriteString("# Written by NexoVirt Box. Changes here are overwritten.\n[Interface]\n")
	fmt.Fprintf(&b, "Address = %s/24\nListenPort = %d\nPrivateKey = %s\n", hostIn(s.Subnet, 1), s.Port, priv)
	fmt.Fprintf(&b, "PostUp = nft -f %s\n", vpnNftPath)
	// Docker sets the iptables FORWARD policy to DROP; let VPN traffic to the LAN and its answers through.
	fmt.Fprintf(&b, "PostUp = iptables -I DOCKER-USER 1 -i %%i -d %s -j ACCEPT 2>/dev/null || true\n", s.LAN)
	b.WriteString("PostUp = iptables -I DOCKER-USER 1 -o %i -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || true\n")
	b.WriteString("PostDown = nft delete table inet nxbox_vpn 2>/dev/null || true\n")
	fmt.Fprintf(&b, "PostDown = iptables -D DOCKER-USER -i %%i -d %s -j ACCEPT 2>/dev/null || true\n", s.LAN)
	b.WriteString("PostDown = iptables -D DOCKER-USER -o %i -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || true\n")
	for _, c := range s.Clients {
		fmt.Fprintf(&b, "\n[Peer]\n# %s\nPublicKey = %s\nAllowedIPs = %s/32\n", c.Name, c.Pub, c.IP)
	}
	return b.String()
}

// vpnNft: VPN clients may reach the box itself (input is not filtered here) and the LAN only.
func vpnNft(s vpnState) string {
	return "add table inet nxbox_vpn\ndelete table inet nxbox_vpn\ntable inet nxbox_vpn {\n" +
		"\tchain vpnfwd {\n\t\ttype filter hook forward priority -5; policy accept;\n" +
		"\t\tiifname \"nxbox0\" ip daddr " + s.LAN + " accept\n\t\tiifname \"nxbox0\" drop\n\t}\n" +
		"\tchain vpnnat {\n\t\ttype nat hook postrouting priority 100; policy accept;\n" +
		"\t\tip saddr " + s.Subnet + " ip daddr " + s.LAN + " masquerade\n\t}\n}\n"
}

// clientConf is what the client imports: only the VPN subnet and the LAN go through the tunnel.
func clientConf(s vpnState, c vpnClient, priv string) string {
	return fmt.Sprintf("[Interface]\nPrivateKey = %s\nAddress = %s/32\n\n[Peer]\nPublicKey = %s\nEndpoint = %s:%d\nAllowedIPs = %s, %s\nPersistentKeepalive = 25\n",
		priv, c.IP, s.ServerPub, s.Endpoint, s.Port, s.Subnet, s.LAN)
}

func serverPriv() (string, error) {
	b, err := os.ReadFile(wgConf)
	if err != nil {
		return "", err
	}
	for _, l := range strings.Split(string(b), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok && strings.TrimSpace(k) == "PrivateKey" {
			v = strings.TrimSpace(v)
			if wgKeyRe.MatchString(v) {
				return v, nil
			}
		}
	}
	return "", errors.New("no server key")
}

// applyVPN writes every file and brings the interface in line.
func applyVPN(s vpnState, priv string, restart bool) error {
	if err := os.MkdirAll(filepath.Dir(wgConf), 0o700); err != nil {
		return err
	}
	if err := writeFileAtomic(wgConf, []byte(serverConf(s, priv)), 0o600); err != nil {
		return err
	}
	if err := writeFileAtomic(vpnNftPath, []byte(vpnNft(s)), 0o600); err != nil {
		return err
	}
	if err := writeJSONFile(vpnStatePath, s); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if restart || exec.CommandContext(ctx, "systemctl", "is-active", "--quiet", "wg-quick@nxbox0").Run() != nil {
		exec.CommandContext(ctx, "systemctl", "enable", "wg-quick@nxbox0").Run()
		if out, err := exec.CommandContext(ctx, "systemctl", "restart", "wg-quick@nxbox0").CombinedOutput(); err != nil {
			return uerr("The VPN did not start: %s", strings.TrimSpace(lastLine(string(out))))
		}
		return nil
	}
	// Running: load the peers without dropping connections.
	stripped, err := exec.CommandContext(ctx, "wg-quick", "strip", "nxbox0").Output()
	if err != nil {
		return uerr("The VPN configuration could not be read back.")
	}
	tmp := filepath.Join(filepath.Dir(wgConf), ".nxbox0.strip")
	if err := os.WriteFile(tmp, stripped, 0o600); err != nil {
		return err
	}
	defer os.Remove(tmp)
	if out, err := exec.CommandContext(ctx, "wg", "syncconf", "nxbox0", tmp).CombinedOutput(); err != nil {
		return uerr("The VPN clients could not be updated: %s", strings.TrimSpace(lastLine(string(out))))
	}
	return nil
}

func checkEndpoint(e string) error {
	if net.ParseIP(e) == nil && (!endpointRe.MatchString(e) || !strings.Contains(e, ".")) {
		return uerr("Enter the address clients use to reach this box: a domain name or a public IP address.")
	}
	return nil
}

// ---- helper actions ----

func (h *helperServer) vpnStatus(req helperRequest, _ func(string)) (any, error) {
	_, wgErr := exec.LookPath("wg")
	s, on := readVPN()
	st := map[string]any{"installed": wgErr == nil, "enabled": on, "lan_ip": serverIP()}
	if !on {
		st["suggest_lan"] = lanOf(serverIP())
		return st, nil
	}
	hs := map[string]int64{}
	if out, err := exec.Command("wg", "show", "nxbox0", "latest-handshakes").Output(); err == nil {
		for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			f := strings.Fields(l)
			if len(f) == 2 {
				n, _ := strconv.ParseInt(f[1], 10, 64)
				hs[f[0]] = n
			}
		}
	}
	clients := []map[string]any{}
	for _, c := range s.Clients {
		clients = append(clients, map[string]any{"name": c.Name, "ip": c.IP, "created": c.Created, "handshake": hs[c.Pub]})
	}
	st["port"], st["endpoint"], st["subnet"], st["lan"], st["clients"] = s.Port, s.Endpoint, s.Subnet, s.LAN, clients
	st["running"] = exec.Command("systemctl", "is-active", "--quiet", "wg-quick@nxbox0").Run() == nil
	return st, nil
}

// vpnSetup turns the VPN on (Port, Domain = endpoint) or off.
func (h *helperServer) vpnSetup(req helperRequest, _ func(string)) (any, error) {
	if !h.consoleAllowed(req) {
		return nil, uerr("Enter your password to change the VPN.")
	}
	if req.Op == "off" {
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		exec.CommandContext(ctx, "systemctl", "disable", "--now", "wg-quick@nxbox0").Run()
		for _, f := range []string{wgConf, vpnNftPath, vpnStatePath} {
			os.Remove(f)
		}
		return map[string]bool{"ok": true}, nil
	}
	if _, err := exec.LookPath("wg-quick"); err != nil {
		return nil, uerr("needs_wireguard: Install WireGuard first.")
	}
	port := req.HTTPPort
	if port == 0 {
		port = 51820
	}
	if port < 1024 || port > 65535 {
		return nil, uerr("Pick a UDP port between 1024 and 65535.")
	}
	endpoint := strings.TrimSpace(req.Domain)
	if err := checkEndpoint(endpoint); err != nil {
		return nil, err
	}
	s, on := readVPN()
	priv := ""
	if on {
		p, err := serverPriv()
		if err != nil {
			return nil, err
		}
		priv = p
	} else {
		lan := lanOf(serverIP())
		if lan == "" {
			return nil, uerr("Box could not find this server's local network.")
		}
		var taken []string
		addrs, _ := net.InterfaceAddrs()
		for _, a := range addrs {
			taken = append(taken, a.String())
		}
		var pub string
		var err error
		if priv, pub, err = wgKeyPair(); err != nil {
			return nil, err
		}
		sub := pickSubnet(taken)
		if sub == "" {
			return nil, uerr("Box found no free private network for the VPN.")
		}
		s = vpnState{Subnet: sub, LAN: lan, ServerPub: pub, Clients: []vpnClient{}}
	}
	restart := !on || s.Port != port
	s.Port, s.Endpoint = port, endpoint
	os.WriteFile(vpnSysctl, []byte("# Written by NexoVirt Box for its VPN.\nnet.ipv4.ip_forward = 1\n"), 0o644)
	exec.Command("sysctl", "-q", "-w", "net.ipv4.ip_forward=1").Run()
	if err := applyVPN(s, priv, restart); err != nil {
		return nil, err
	}
	return h.vpnStatus(req, nil)
}

// vpnClientAdd makes a client and returns its config once (never stored).
func (h *helperServer) vpnClientAdd(req helperRequest, _ func(string)) (any, error) {
	if !h.consoleAllowed(req) {
		return nil, uerr("Enter your password to add a VPN device.")
	}
	s, on := readVPN()
	if !on {
		return nil, uerr("Turn the VPN on first.")
	}
	name := strings.TrimSpace(req.Name)
	if !vpnNameRe.MatchString(name) {
		return nil, uerr("Give the device a name of up to 40 letters, digits, spaces, dots and dashes.")
	}
	used := map[string]bool{hostIn(s.Subnet, 1): true}
	for _, c := range s.Clients {
		if strings.EqualFold(c.Name, name) {
			return nil, uerr("A device called %s exists already.", name)
		}
		used[c.IP] = true
	}
	ip := ""
	for n := 2; n < 255; n++ {
		if !used[hostIn(s.Subnet, n)] {
			ip = hostIn(s.Subnet, n)
			break
		}
	}
	if ip == "" {
		return nil, uerr("The VPN has no free address left.")
	}
	priv, pub, err := wgKeyPair()
	if err != nil {
		return nil, err
	}
	c := vpnClient{Name: name, Pub: pub, IP: ip, Created: time.Now().Unix()}
	s.Clients = append(s.Clients, c)
	sp, err := serverPriv()
	if err != nil {
		return nil, err
	}
	if err := applyVPN(s, sp, false); err != nil {
		return nil, err
	}
	return map[string]any{"name": name, "ip": ip, "config": clientConf(s, c, priv)}, nil
}

func (h *helperServer) vpnClientRevoke(req helperRequest, _ func(string)) (any, error) {
	s, on := readVPN()
	if !on {
		return nil, uerr("The VPN is off.")
	}
	var keep []vpnClient
	found := false
	for _, c := range s.Clients {
		if c.Name == req.Name {
			found = true
			continue
		}
		keep = append(keep, c)
	}
	if !found {
		return nil, uerr("This device does not exist.")
	}
	s.Clients = keep
	if s.Clients == nil {
		s.Clients = []vpnClient{}
	}
	sp, err := serverPriv()
	if err != nil {
		return nil, err
	}
	if err := applyVPN(s, sp, false); err != nil {
		return nil, err
	}
	return map[string]bool{"ok": true}, nil
}

// ---- web ----

func (a *App) vpnGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	raw, err := a.helper.call(ctx, helperRequest{Action: "vpn_status"}, nil)
	if err != nil {
		writeError(w, http.StatusServiceUnavailable, friendly(err))
		return
	}
	var st map[string]any
	if err := json.Unmarshal(raw, &st); err != nil {
		writeError(w, http.StatusInternalServerError, "The VPN status could not be read.")
		return
	}
	// Behind NAT the router must forward the UDP port: the public address differs from the LAN one.
	pctx, pcancel := context.WithTimeout(r.Context(), 5*time.Second)
	pub := publicIP(pctx)
	pcancel()
	st["public_ip"] = pub
	st["behind_nat"] = pub != "" && pub != st["lan_ip"]
	writeJSON(w, http.StatusOK, st)
}

func (a *App) vpnSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		On       bool
		Port     int
		Endpoint string
	}
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	op := "off"
	if in.On {
		op = "on"
	}
	if !a.needProof(w, r) {
		return
	}
	a.vpnCall(w, r, a.proofReq(r, helperRequest{Action: "vpn_setup", Op: op, HTTPPort: in.Port, Domain: in.Endpoint}), "vpn."+op, in.Endpoint)
}

func (a *App) vpnClientAddWeb(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name string }
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !a.needProof(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	raw, err := a.helper.call(ctx, a.proofReq(r, helperRequest{Action: "vpn_client_add", Name: in.Name}), nil)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	var res struct {
		Name, IP, Config string
	}
	json.Unmarshal(raw, &res)
	qr, _ := qrDataURI(res.Config)
	a.audit(r, "vpn.client_add", res.Name+" "+res.IP) // never the config: it holds the private key
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]string{"name": res.Name, "ip": res.IP, "config": res.Config, "qr": qr})
}

func (a *App) vpnClientRevokeWeb(w http.ResponseWriter, r *http.Request) {
	var in struct{ Name string }
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.vpnCall(w, r, helperRequest{Action: "vpn_client_revoke", Name: in.Name}, "vpn.client_revoke", in.Name)
}

func (a *App) vpnCall(w http.ResponseWriter, r *http.Request, req helperRequest, audit, detail string) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	raw, err := a.helper.call(ctx, req, nil)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	a.audit(r, audit, detail)
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}
