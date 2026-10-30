package box

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The sign-in in front of an app: a one-time token works once and only on its domain, the
// cookie only opens that domain, and both die with the Box session.
func TestProxyAuthFlow(t *testing.T) {
	app, h, token := newTestApp(t)
	sess := sessionFrom(post(h, "/api/setup", `{"token":"`+token+`","username":"admin","password":"long-enough-pw"}`, ""))
	once := app.store.addProxyToken("app.example.com", hashToken(sess), "once", proxyOnceTTL)

	finish := func(host, tok string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "https://"+host+"/api/proxy-auth/finish?t="+tok+"&rd=//evil.example/x", nil)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	if rec := finish("other.example.com", once); rec.Code != http.StatusForbidden {
		t.Fatalf("token used on another domain: %d", rec.Code)
	}
	rec := finish("app.example.com", once)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/" {
		t.Fatalf("finish: %d %q (redirects must stay on the site)", rec.Code, rec.Header().Get("Location"))
	}
	var cookie string
	for _, c := range rec.Result().Cookies() {
		if c.Name == proxyCookie && c.HttpOnly && c.Secure {
			cookie = c.Value
		}
	}
	if cookie == "" || cookie == sess {
		t.Fatal("no separate domain cookie set")
	}
	if rec := finish("app.example.com", once); rec.Code != http.StatusForbidden {
		t.Fatal("one-time token worked twice")
	}

	check := func(host, c string) int {
		r := httptest.NewRequest("GET", "https://127.0.0.1/api/proxy-auth/check", nil)
		r.Header.Set("X-Forwarded-Host", host)
		r.Header.Set("X-Forwarded-Uri", "/dash")
		if c != "" {
			r.AddCookie(&http.Cookie{Name: proxyCookie, Value: c})
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec.Code
	}
	if check("app.example.com", cookie) != http.StatusOK {
		t.Fatal("valid cookie refused")
	}
	if check("other.example.com", cookie) == http.StatusOK || check("app.example.com", "") == http.StatusOK {
		t.Fatal("cookie opened another domain, or no cookie passed")
	}
	// The domain cookie is not a Box session.
	if get(h, "/api/system", cookie).Code != http.StatusUnauthorized {
		t.Fatal("the domain cookie works as a Box session")
	}
	app.store.setSetting(proxyBoxURLSave, "https://box.example.com:8443")
	r := httptest.NewRequest("GET", "https://127.0.0.1/api/proxy-auth/check", nil)
	r.Header.Set("X-Forwarded-Host", "app.example.com")
	r.Header.Set("X-Forwarded-Uri", "/dash")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, r)
	if loc := rr.Header().Get("Location"); rr.Code != http.StatusFound || !strings.HasPrefix(loc, "https://box.example.com:8443/api/proxy-auth/start?rd=https%3A%2F%2Fapp.example.com%2Fdash") {
		t.Fatalf("no redirect to Box sign-in: %d %q", rr.Code, loc)
	}
	// Signing out of Box ends the domain cookie too.
	post(h, "/api/logout", "", sess)
	if check("app.example.com", cookie) == http.StatusOK {
		t.Fatal("cookie still valid after sign-out")
	}
	// start only issues tokens for protected app domains.
	sess2 := sessionFrom(post(h, "/api/login", `{"username":"admin","password":"long-enough-pw"}`, ""))
	if rec := get(h, "/api/proxy-auth/start?rd="+"https%3A%2F%2Fevil.example%2F", sess2); rec.Code != http.StatusBadRequest {
		t.Fatalf("start for an unknown domain: %d", rec.Code)
	}
}

func TestSafeLocalPath(t *testing.T) {
	for in, want := range map[string]string{
		"/a?b=1": "/a?b=1", "/dash/x": "/dash/x", "//evil": "/", "https://evil": "/", "/\\evil": "/", "": "/",
		"/x\r\nSet-Cookie: a": "/", "/\t/evil.com": "/", "/\x00/evil": "/", "/a\x7f": "/", "/a\\b": "/", "/\n/evil": "/",
		"/%09/evil.com": "/%09/evil.com", // encoded stays a path on the same site
	} {
		if got := safeLocalPath(in); got != want {
			t.Errorf("safeLocalPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// The one-time token only ever goes to a protected domain on the proxy's HTTPS port.
func TestSigninTarget(t *testing.T) {
	prot := map[string]bool{"app.example.com": true}
	ok := map[string]string{
		"https://app.example.com/dash":     "https://app.example.com",
		"https://APP.example.com:443/dash": "https://app.example.com",
	}
	for rd, want := range ok {
		base, domain, path, good := signinTarget(rd, prot, 443)
		if !good || base != want || domain != "app.example.com" || path != "/dash" {
			t.Errorf("%s: %v %q %q %q", rd, good, base, domain, path)
		}
	}
	if base, _, _, good := signinTarget("https://app.example.com:8444/", prot, 8444); !good || base != "https://app.example.com:8444" {
		t.Errorf("own proxy port refused: %q", base)
	}
	for _, rd := range []string{
		"https://app.example.com:8080/", // another service on that host
		"https://user:pw@app.example.com/",
		"https://evil.example/@app.example.com",
		"http://app.example.com/",
		"https://other.example.com/",
		"//app.example.com/",
		"https://app.example.com:443@evil.example/",
	} {
		if _, _, _, good := signinTarget(rd, prot, 443); good {
			t.Errorf("%s accepted", rd)
		}
	}
}

func TestCaddyfileProtectedRoute(t *testing.T) {
	old := proxyDir
	proxyDir = t.TempDir()
	t.Cleanup(func() { proxyDir = old })
	h := &helperServer{appsDir: t.TempDir()}
	for _, m := range []installedApp{
		{ID: "wiki", MainPort: 8080, Domain: "wiki.example.com", TLS: "letsencrypt", Protect: true, DomainPort: 8081},
		{ID: "blog", MainPort: 9000, Domain: "blog.example.com", TLS: "internal"},
	} {
		os.MkdirAll(h.appDir(m.ID), 0o700)
		h.writeMeta(m)
	}
	if err := h.writeCaddyfile(proxyConfig{HTTPPort: 80, HTTPSPort: 443, BoxPort: 9443}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(proxyDir, "Caddyfile"))
	s := string(b)
	for _, want := range []string{"forward_auth https://127.0.0.1:9443", "uri /api/proxy-auth/check", "reverse_proxy 127.0.0.1:8081", "handle /.nxbox-auth",
		"blog.example.com {\n\ttls internal\n\treverse_proxy 127.0.0.1:9000\n}"} {
		if !strings.Contains(s, want) {
			t.Fatalf("Caddyfile lacks %q:\n%s", want, s)
		}
	}
	if strings.Count(s, "forward_auth") != 1 {
		t.Fatalf("the unprotected app got a sign-in:\n%s", s)
	}
}
