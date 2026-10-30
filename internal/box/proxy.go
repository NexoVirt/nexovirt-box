package box

// Reverse proxy + HTTPS for apps: one Caddy container (`nxbox-proxy`, host network) managed by
// the helper, with its own files in /etc/nexovirt-box/caddy. It never touches another web
// server: when 80/443 are taken, Box offers other ports (or none at all). Each app can get a
// domain; Let's Encrypt is used when the domain points to this server and Caddy holds 80 or
// 443, otherwise Caddy's own local certificate (tls internal).

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

var proxyDir = "/etc/nexovirt-box/caddy"

const proxyImage = "caddy:2"

type proxyConfig struct {
	HTTPPort  int    `json:"http_port"`
	HTTPSPort int    `json:"https_port"`
	Email     string `json:"email,omitempty"`
	BoxPort   int    `json:"box_port,omitempty"` // Box's own HTTPS port, for the sign-in gate (proxyauth.go)
}

type proxyRoute struct {
	App     string    `json:"app"`
	Domain  string    `json:"domain"`
	Port    int       `json:"port"`
	TLS     string    `json:"tls"`               // letsencrypt | internal
	Protect bool      `json:"protect,omitempty"` // Box sign-in in front of the app
	Cert    *certInfo `json:"cert,omitempty"`
}

type certInfo struct {
	Issuer  string `json:"issuer"`
	Expires int64  `json:"expires"`
}

// routeCert reads the certificate Caddy keeps for a domain (any issuer folder).
func routeCert(domain string) *certInfo {
	files, _ := filepath.Glob(filepath.Join(proxyDir, "data", "caddy", "certificates", "*", domain, domain+".crt"))
	var best *certInfo
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		blk, _ := pem.Decode(b)
		if blk == nil {
			continue
		}
		c, err := x509.ParseCertificate(blk.Bytes)
		if err != nil {
			continue
		}
		if best == nil || c.NotAfter.Unix() > best.Expires {
			issuer := c.Issuer.Organization
			name := c.Issuer.CommonName
			if len(issuer) > 0 {
				name = issuer[0]
			}
			best = &certInfo{Issuer: name, Expires: c.NotAfter.Unix()}
		}
	}
	return best
}

func readProxyConfig() (proxyConfig, bool) {
	var c proxyConfig
	b, err := os.ReadFile(filepath.Join(proxyDir, "proxy.json"))
	if err != nil {
		return c, false
	}
	return c, json.Unmarshal(b, &c) == nil && c.HTTPSPort > 0
}

func proxyCompose(args ...string) *exec.Cmd {
	base := []string{"compose", "--project-name", "nxbox-proxy", "--project-directory", proxyDir, "-f", filepath.Join(proxyDir, "compose.yaml")}
	return exec.Command("docker", append(base, args...)...)
}

// proxyRunning reports whether Box's Caddy container runs.
func proxyRunning() bool {
	out, err := exec.Command("docker", "inspect", "-f", "{{.State.Running}}", "nxbox-proxy").Output()
	return err == nil && strings.TrimSpace(string(out)) == "true"
}

func (h *helperServer) proxyStatus(req helperRequest, logf func(string)) (any, error) {
	cfg, ok := readProxyConfig()
	routes := h.proxyRoutes()
	for i := range routes {
		routes[i].Cert = routeCert(routes[i].Domain)
	}
	st := map[string]any{"enabled": ok, "running": false, "routes": routes}
	if ok {
		st["config"] = cfg
		st["running"] = needDocker() == nil && proxyRunning()
	}
	// Free ports, so the UI can offer the standard ones only when nothing else holds them.
	ownPorts := ok && st["running"] == true
	st["port80_free"] = (ownPorts && cfg.HTTPPort == 80) || portFree("tcp", 80)
	st["port443_free"] = (ownPorts && cfg.HTTPSPort == 443) || portFree("tcp", 443)
	return st, nil
}

func (h *helperServer) proxyRoutes() []proxyRoute {
	entries, _ := os.ReadDir(h.appsDir)
	out := []proxyRoute{}
	for _, e := range entries {
		m, err := h.readMeta(e.Name())
		if err == nil && m.Domain != "" {
			port := m.MainPort
			if m.DomainPort > 0 {
				port = m.DomainPort
			}
			out = append(out, proxyRoute{App: m.ID, Domain: m.Domain, Port: port, TLS: m.TLS, Protect: m.Protect})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Domain < out[j].Domain })
	return out
}

func validPort(p int) bool { return p >= 1 && p <= 65535 }

// proxySetup starts (or reconfigures) Box's Caddy on the given ports.
func (h *helperServer) proxySetup(req helperRequest, logf func(string)) (any, error) {
	if err := needDocker(); err != nil {
		return nil, err
	}
	cfg := proxyConfig{HTTPPort: req.HTTPPort, HTTPSPort: req.HTTPSPort, Email: strings.TrimSpace(req.Email)}
	if !validPort(cfg.HTTPPort) || !validPort(cfg.HTTPSPort) || cfg.HTTPPort == cfg.HTTPSPort {
		return nil, uerr("Pick two different ports between 1 and 65535.")
	}
	if cfg.Email != "" && !emailRe.MatchString(cfg.Email) {
		return nil, uerr("The email address is not valid.")
	}
	old, had := readProxyConfig()
	running := had && proxyRunning()
	for _, p := range []int{cfg.HTTPPort, cfg.HTTPSPort} {
		mine := running && (p == old.HTTPPort || p == old.HTTPSPort)
		if !mine && !portFree("tcp", p) {
			return nil, uerr("Port %d is already used by another program on this server. Box does not take it over; pick another port.", p)
		}
	}
	if err := os.MkdirAll(filepath.Join(proxyDir, "data"), 0o700); err != nil {
		return nil, err
	}
	os.MkdirAll(filepath.Join(proxyDir, "config"), 0o700)
	b, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(filepath.Join(proxyDir, "proxy.json"), b, 0o600); err != nil {
		return nil, err
	}
	composeFile := fmt.Sprintf(`# Written by NexoVirt Box: its reverse proxy for apps.
services:
  caddy:
    image: %s
    container_name: nxbox-proxy
    network_mode: host
    restart: unless-stopped
    volumes:
      - ./Caddyfile:/etc/caddy/Caddyfile:ro
      - ./data:/data
      - ./config:/config
`, proxyImage)
	if err := os.WriteFile(filepath.Join(proxyDir, "compose.yaml"), []byte(composeFile), 0o600); err != nil {
		return nil, err
	}
	if err := h.writeCaddyfile(cfg); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	logf("Starting the reverse proxy (Caddy) on ports " + strconv.Itoa(cfg.HTTPPort) + " and " + strconv.Itoa(cfg.HTTPSPort))
	if err := runStream(ctx, proxyCompose("up", "-d", "--force-recreate"), logf); err != nil {
		return nil, err
	}
	return h.proxyStatus(req, logf)
}

func (h *helperServer) proxyRemove(req helperRequest, logf func(string)) (any, error) {
	if _, ok := readProxyConfig(); !ok {
		return nil, uerr("The reverse proxy is not set up.")
	}
	if needDocker() == nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if err := runStream(ctx, proxyCompose("down"), logf); err != nil {
			return nil, err
		}
	}
	os.Remove(filepath.Join(proxyDir, "proxy.json"))
	return h.proxyStatus(req, logf)
}

// writeCaddyfile renders every app route. Only Box's own file is written.
func (h *helperServer) writeCaddyfile(cfg proxyConfig) error {
	var b strings.Builder
	b.WriteString("# Written by NexoVirt Box. Changes here are overwritten.\n{\n")
	fmt.Fprintf(&b, "\thttp_port %d\n\thttps_port %d\n\tadmin 127.0.0.1:2019\n", cfg.HTTPPort, cfg.HTTPSPort)
	if cfg.Email != "" {
		fmt.Fprintf(&b, "\temail %s\n", cfg.Email)
	}
	b.WriteString("}\n")
	boxPort := cfg.BoxPort
	if boxPort == 0 {
		boxPort = 8443
	}
	for _, r := range h.proxyRoutes() {
		fmt.Fprintf(&b, "\n%s {\n", r.Domain)
		if r.TLS != "letsencrypt" {
			b.WriteString("\ttls internal\n")
		}
		if r.Protect {
			// Box's sign-in in front: the one-time token comes back on the app's own domain, and
			// every request is checked against Box first (proxyauth.go). Box is on localhost with
			// its own self-signed certificate.
			box := fmt.Sprintf("https://127.0.0.1:%d", boxPort)
			fmt.Fprintf(&b, "\thandle /.nxbox-auth {\n\t\trewrite * /api/proxy-auth/finish?{query}\n\t\treverse_proxy %s {\n\t\t\ttransport http {\n\t\t\t\ttls_insecure_skip_verify\n\t\t\t}\n\t\t}\n\t}\n", box)
			fmt.Fprintf(&b, "\thandle {\n\t\tforward_auth %s {\n\t\t\turi /api/proxy-auth/check\n\t\t\ttransport http {\n\t\t\t\ttls_insecure_skip_verify\n\t\t\t}\n\t\t}\n\t\treverse_proxy 127.0.0.1:%d\n\t}\n", box, r.Port)
		} else {
			fmt.Fprintf(&b, "\treverse_proxy 127.0.0.1:%d\n", r.Port)
		}
		b.WriteString("}\n")
	}
	return os.WriteFile(filepath.Join(proxyDir, "Caddyfile"), []byte(b.String()), 0o644)
}

func (h *helperServer) reloadProxy(logf func(string)) error {
	cfg, ok := readProxyConfig()
	if !ok {
		return nil
	}
	if err := h.writeCaddyfile(cfg); err != nil {
		return err
	}
	if !proxyRunning() {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	return runStream(ctx, exec.Command("docker", "exec", "nxbox-proxy", "caddy", "reload", "--config", "/etc/caddy/Caddyfile", "--adapter", "caddyfile"), logf)
}

// appDomain sets or removes an app's domain and decides how its certificate is made.
func (h *helperServer) appDomain(req helperRequest, logf func(string)) (any, error) {
	if err := validID(req.ID); err != nil {
		return nil, err
	}
	defer h.appLock(req.ID)()
	meta, err := h.readMeta(req.ID)
	if err != nil {
		return nil, err
	}
	cfg, ok := readProxyConfig()
	domain := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(req.Domain), "."))
	if domain != "" {
		if !ok {
			return nil, uerr("Set up the reverse proxy first (Settings).")
		}
		if !domainRe.MatchString(domain) || !strings.Contains(domain, ".") || net.ParseIP(domain) != nil {
			return nil, uerr("Enter a domain name like app.example.com.")
		}
		for _, r := range h.proxyRoutes() {
			if r.Domain == domain && r.App != meta.ID {
				return nil, uerr("%s is already used by another app.", domain)
			}
		}
	}
	port := 0
	if domain != "" && req.Port > 0 && req.Port != meta.MainPort {
		if !validPort(req.Port) {
			return nil, uerr("Pick a port between 1 and 65535.")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		cfgJSON, err := renderConfig(ctx, h.appDir(meta.ID), meta.ID)
		cancel()
		if err != nil {
			return nil, err
		}
		ports, _ := publishedPorts(cfgJSON)
		found := false
		for _, hp := range ports {
			found = found || (hp.proto == "tcp" && hp.port == req.Port)
		}
		if !found {
			return nil, uerr("%s does not publish port %d. Pick one of its ports.", meta.Name, req.Port)
		}
		port = req.Port
	}
	meta.Domain, meta.TLS, meta.DomainPort, meta.Protect = domain, "", port, domain != "" && req.Confirm == "protect"
	if meta.Protect && meta.DomainPort == 0 {
		meta.DomainPort = meta.MainPort // pin the protected port, so a later edit cannot move it
	}
	if meta.Protect && meta.DomainPort <= 0 {
		return nil, uerr("%s has no web port Box knows, so the sign-in cannot keep it private. Enter the app port behind the domain first.", meta.Name)
	}
	if err := h.applyBinding(meta, logf); err != nil {
		return nil, err
	}
	if req.HTTPPort > 0 && validPort(req.HTTPPort) && ok && cfg.BoxPort != req.HTTPPort {
		cfg.BoxPort = req.HTTPPort // the web process sends its own port
		if b, err := json.MarshalIndent(cfg, "", "  "); err == nil {
			os.WriteFile(filepath.Join(proxyDir, "proxy.json"), b, 0o600)
		}
	}
	note := ""
	if domain != "" {
		points := dnsPointsHere(domain)
		acme := cfg.HTTPPort == 80 || cfg.HTTPSPort == 443
		switch {
		case points && acme:
			meta.TLS = "letsencrypt"
			note = "The domain points to this server: Caddy gets a Let's Encrypt certificate."
		case points:
			meta.TLS = "internal"
			note = "The domain points here, but Let's Encrypt needs port 80 or 443 for the proxy. Using a local certificate."
		default:
			meta.TLS = "internal"
			note = "The domain does not point to this server yet. Using a local certificate (browsers will warn). Set a DNS record to this server and save again for Let's Encrypt."
		}
	}
	b, _ := json.MarshalIndent(meta, "", "  ")
	if err := os.WriteFile(filepath.Join(h.appDir(meta.ID), "app.json"), b, 0o600); err != nil {
		return nil, err
	}
	if err := h.reloadProxy(logf); err != nil {
		return nil, err
	}
	return map[string]string{"domain": domain, "tls": meta.TLS, "note": note}, nil
}

// dnsPointsHere checks the domain's A/AAAA records against this server's addresses: the local
// interfaces, and the public address as seen from outside (api.ipify.org) for servers behind NAT.
func dnsPointsHere(domain string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, domain)
	if err != nil || len(ips) == 0 {
		return false
	}
	mine := map[string]bool{}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				mine[n.IP.String()] = true
			}
		}
	}
	if pub := publicIP(ctx); pub != "" {
		mine[pub] = true
	}
	for _, ip := range ips {
		if mine[ip.IP.String()] {
			return true
		}
	}
	return false
}

func publicIP(ctx context.Context) string {
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.ipify.org", nil)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 64))
	if ip := net.ParseIP(strings.TrimSpace(string(b))); ip != nil {
		return ip.String()
	}
	return ""
}
