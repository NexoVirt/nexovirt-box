package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/NexoVirt/nexovirt-box/internal/catalog"
)

func TestParseRefAndVars(t *testing.T) {
	for in, want := range map[string]string{
		"nginx":                             "registry-1.docker.io/library/nginx:latest",
		"minio/minio:RELEASE.2025-09-07T16": "registry-1.docker.io/minio/minio:RELEASE.2025-09-07T16",
		"ghcr.io/Foo/bar:1.2@sha256:ab":     "ghcr.io/foo/bar@sha256:ab",
		"lscr.io/linuxserver/x":             "lscr.io/linuxserver/x:latest",
	} {
		r, ok := parseRef(in)
		if !ok || refKey(r) != want {
			t.Errorf("%s: got %s", in, refKey(r))
		}
	}
	if s, ok := resolveVars("app:${TAG:-1.0}"); !ok || s != "app:1.0" {
		t.Errorf("default: %q %v", s, ok)
	}
	if _, ok := resolveVars("app:${TAG}"); ok {
		t.Error("a variable without a default must not resolve")
	}
}

func testChecker(srv *httptest.Server) *checker {
	c := newChecker()
	c.client, c.hubAPI, c.sleep = srv.Client(), srv.URL, func(time.Duration) {}
	return c
}

func TestRegistryStatuses(t *testing.T) {
	var host string
	limited := 1
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/v2/repositories/library/nginx/tags/latest":
			w.Write([]byte(`{"images":[{"architecture":"amd64","os":"linux"},{"architecture":"arm64","os":"linux"}]}`))
		case strings.HasPrefix(r.URL.Path, "/v2/repositories/"):
			w.WriteHeader(404)
		case r.URL.Path == "/token":
			w.Write([]byte(`{"token":"t"}`))
		case r.Header.Get("Authorization") != "Bearer t":
			w.Header().Set("Www-Authenticate", `Bearer realm="https://`+host+`/token",service="reg"`)
			w.WriteHeader(401)
		case r.URL.Path == "/v2/ok/app/manifests/1":
			if limited > 0 {
				limited--
				w.WriteHeader(429)
				return
			}
			w.Write([]byte(`{"manifests":[{"platform":{"architecture":"amd64","os":"linux"}},{"platform":{"architecture":"unknown","os":"unknown"}}]}`))
		case r.URL.Path == "/v2/single/app/manifests/1":
			w.Write([]byte(`{"config":{"digest":"sha256:c"}}`))
		case r.URL.Path == "/v2/single/app/blobs/sha256:c":
			w.Write([]byte(`{"architecture":"arm64"}`))
		case strings.HasPrefix(r.URL.Path, "/v2/private/"):
			w.WriteHeader(403)
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	host = srv.Listener.Addr().String()
	c := testChecker(srv)
	ctx := context.Background()
	check := func(repo string) result { return c.check(ctx, ref{host: host, repo: repo, tag: "1"}) }
	if r := check("ok/app"); r.Status != "ok" || !slices.Equal(r.Archs, []string{"amd64"}) {
		t.Errorf("index after a 429: %+v", r)
	}
	if r := check("single/app"); r.Status != "ok" || !slices.Equal(r.Archs, []string{"arm64"}) {
		t.Errorf("single arch: %+v", r)
	}
	if r := check("gone/app"); r.Status != "missing" {
		t.Errorf("missing: %+v", r)
	}
	if r := check("private/app"); r.Status != "private" {
		t.Errorf("private: %+v", r)
	}
	if r := c.check(ctx, ref{host: hubRegistry, repo: "library/nginx", tag: "latest"}); r.Status != "ok" || len(r.Archs) != 2 {
		t.Errorf("hub: %+v", r)
	}
	if r := c.check(ctx, ref{host: hubRegistry, repo: "gone/x", tag: "1"}); r.Status != "missing" {
		t.Errorf("hub missing: %+v", r)
	}
}

func TestDecide(t *testing.T) {
	mk := func(res ...result) *appReport {
		rep := &appReport{App: &catalog.App{Summary: catalog.Summary{Architectures: []string{"amd64", "arm64", "armv7"}}}}
		for _, r := range res {
			rep.Images = append(rep.Images, &imageInfo{Raw: "x", OK: true, Res: r})
		}
		return rep
	}
	rep := mk(result{Status: "ok", Archs: []string{"amd64", "arm64"}}, result{Status: "ok", Archs: []string{"amd64"}})
	decide(rep)
	if rep.Drop != "" || !slices.Equal(rep.App.Architectures, []string{"amd64"}) {
		t.Errorf("intersection: %+v %v", rep, rep.App.Architectures)
	}
	rep = mk(result{Status: "private"}, result{Status: "missing"})
	decide(rep)
	if rep.Drop != "missing" {
		t.Errorf("missing wins: %q", rep.Drop)
	}
	rep = mk(result{Status: "rate-limited"}, result{Status: "error"})
	decide(rep)
	if rep.Drop != "" || len(rep.App.Architectures) != 3 {
		t.Errorf("unchecked images never drop or narrow: %+v", rep)
	}
	rep = mk(result{Status: "ok", Archs: []string{"arm"}})
	decide(rep)
	if rep.Drop != "unsupported-arch" {
		t.Errorf("no box arch: %q", rep.Drop)
	}
	rep = mk(result{Status: "ok", Archs: []string{"amd64", "arm64"}})
	rep.App.Architectures = []string{"amd64"}
	decide(rep)
	if !slices.Equal(rep.App.Architectures, []string{"amd64"}) {
		t.Errorf("never wider than the store: %v", rep.App.Architectures)
	}
}
