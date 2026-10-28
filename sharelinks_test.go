package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func createLink(t *testing.T, h http.Handler, sess string, body map[string]any) (int, map[string]any) {
	t.Helper()
	rec := fpost(h, "/api/share-links", body, sess)
	var out map[string]any
	json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func linkPath(t *testing.T, out map[string]any) string {
	u, err := url.Parse(out["url"].(string))
	if err != nil {
		t.Fatal(err)
	}
	return u.Path
}

func formPost(h http.Handler, path, password string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "http://box.test"+path, strings.NewReader(url.Values{"password": {password}}.Encode()))
	req.Header.Set("Origin", "http://box.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = "10.0.0.9:1234"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestShareLinkFileWithLimitAndRevoke(t *testing.T) {
	h, sess, root := filesTestApp(t)
	f := filepath.Join(root, "report.txt")
	os.WriteFile(f, []byte("hello share"), 0o644)

	code, out := createLink(t, h, sess, map[string]any{"path": f, "expires_hours": 24, "max_downloads": 2})
	if code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	p := linkPath(t, out)
	if rec := get(h, p, ""); rec.Code != 200 || !strings.Contains(rec.Body.String(), "report.txt") {
		t.Fatalf("landing: %d %s", rec.Code, rec.Body)
	}
	for i := 0; i < 2; i++ {
		rec := get(h, p+"?download=1", "")
		if rec.Code != 200 || rec.Body.String() != "hello share" || !strings.Contains(rec.Header().Get("Content-Disposition"), "attachment") {
			t.Fatalf("download %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	if rec := get(h, p+"?download=1", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("third download over the limit: %d", rec.Code)
	}

	_, out2 := createLink(t, h, sess, map[string]any{"path": f, "expires_hours": 1})
	p2 := linkPath(t, out2)
	if rec := post(h, "/api/share-links/"+out2["id"].(string)+"/revoke", "{}", sess); rec.Code != 200 {
		t.Fatalf("revoke: %d %s", rec.Code, rec.Body)
	}
	if rec := get(h, p2+"?download=1", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("revoked link: %d", rec.Code)
	}
	// A wrong or unknown token tells nothing.
	if rec := get(h, "/s/"+strings.Repeat("a", 43), ""); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown: %d", rec.Code)
	}
}

func TestShareLinkPasswordExpiryAndRoots(t *testing.T) {
	h, sess, root := filesTestApp(t)
	dir := filepath.Join(root, "photos")
	os.Mkdir(dir, 0o755)
	os.WriteFile(filepath.Join(dir, "a.jpg"), []byte("jpg"), 0o644)

	code, out := createLink(t, h, sess, map[string]any{"path": dir, "expires_hours": 24, "password": "open sesame"})
	if code != 200 {
		t.Fatalf("create: %d %v", code, out)
	}
	p := linkPath(t, out)
	if rec := get(h, p+"?download=1", ""); rec.Code == 200 && rec.Header().Get("Content-Type") == "application/zip" {
		t.Fatal("downloaded without the password")
	}
	if rec := formPost(h, p, "wrong"); rec.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", rec.Code)
	}
	rec := formPost(h, p, "open sesame")
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/zip" {
		t.Fatalf("right password: %d %s", rec.Code, rec.Header())
	}
	zr, err := zip.NewReader(bytes.NewReader(rec.Body.Bytes()), int64(rec.Body.Len()))
	if err != nil || len(zr.File) == 0 {
		t.Fatalf("zip: %v", err)
	}

	// Outside the roots: refused at creation.
	outside := t.TempDir()
	if code, _ := createLink(t, h, sess, map[string]any{"path": outside, "expires_hours": 24}); code == 200 {
		t.Fatal("shared a folder outside the roots")
	}
	// Bad expiry refused.
	if code, _ := createLink(t, h, sess, map[string]any{"path": dir, "expires_hours": 0}); code == 200 {
		t.Fatal("link without expiry")
	}
	// The item is checked again when used: replace the folder with a symlink out of the roots.
	_, out3 := createLink(t, h, sess, map[string]any{"path": dir, "expires_hours": 24})
	os.RemoveAll(dir)
	os.Symlink(outside, dir)
	if rec := get(h, linkPath(t, out3)+"?download=1", ""); rec.Code == 200 {
		t.Fatal("served a folder that now points outside the roots")
	}
}

func TestShareLinkExpired(t *testing.T) {
	app, h, _ := newTestApp(t)
	tok := randomToken()
	app.store.db.Exec(`INSERT INTO share_links (id, token_hash, path, name, is_dir, created_at, expires_at, password_hash, max_downloads, downloads, revoked) VALUES ('deadbeef', ?, '/srv/x', 'x', 0, ?, ?, '', 0, 0, 0)`,
		hashToken(tok), time.Now().Add(-2*time.Hour).Unix(), time.Now().Add(-time.Hour).Unix())
	if rec := get(h, "/s/"+tok, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("expired: %d", rec.Code)
	}
}
