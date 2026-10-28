package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// filesTestApp signs in and limits the file explorer to one temp root.
func filesTestApp(t *testing.T) (http.Handler, string, string) {
	t.Helper()
	old := firewallDir
	firewallDir = t.TempDir()
	t.Cleanup(func() { firewallDir = old })
	root, _ := filepath.EvalSymlinks(t.TempDir())
	writeJSONFile(filepath.Join(firewallDir, "files.json"), filesConfig{Roots: []string{root}})
	_, h, token := newTestApp(t)
	return h, setupAndLogin(t, h, token), root
}

func fpost(h http.Handler, path string, body any, sess string) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	return post(h, path, string(b), sess)
}

func TestFilesStayInsideRoots(t *testing.T) {
	h, sess, root := filesTestApp(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o600)
	os.Symlink(outside, filepath.Join(root, "escape"))
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644)

	if rec := get(h, "/api/files?path="+root, sess); rec.Code != 200 || !strings.Contains(rec.Body.String(), "a.txt") {
		t.Fatalf("list root: %d %s", rec.Code, rec.Body)
	}
	for _, p := range []string{outside, filepath.Join(root, "escape"), filepath.Join(root, "escape", "secret"), "/etc", root + "/../" + filepath.Base(outside)} {
		if rec := get(h, "/api/files?path="+p, sess); rec.Code == 200 {
			t.Fatalf("listed %s", p)
		}
		if rec := get(h, "/api/files/download?path="+p, sess); rec.Code == 200 {
			t.Fatalf("downloaded %s", p)
		}
	}
	// Deleting the symlink removes the link only, never the target.
	if rec := fpost(h, "/api/files/delete", map[string]string{"path": filepath.Join(root, "escape")}, sess); rec.Code != 200 {
		t.Fatalf("delete link: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(filepath.Join(outside, "secret")); err != nil {
		t.Fatal("deleting a symlink removed its target")
	}
	if rec := fpost(h, "/api/files/delete", map[string]string{"path": root}, sess); rec.Code == 200 {
		t.Fatal("deleted a top folder")
	}
	if rec := fpost(h, "/api/files/rename", map[string]string{"path": filepath.Join(root, "a.txt"), "to": filepath.Join(outside, "a.txt")}, sess); rec.Code == 200 {
		t.Fatal("moved a file outside the roots")
	}
	if rec := fpost(h, "/api/files/roots", map[string]string{"roots": "/etc"}, sess); rec.Code == 200 {
		t.Fatal("system folder accepted as a root")
	}
}

func TestFilesUploadDownload(t *testing.T) {
	h, sess, root := filesTestApp(t)
	target := filepath.Join(root, "up.txt")
	send := func(off int, data string, final bool) int {
		u := "/api/files/upload?path=" + target + "&id=0123456789abcdef&offset=" + itoa(off)
		if final {
			u += "&final=1"
		}
		req := httptest.NewRequest("POST", "http://box.test"+u, bytes.NewBufferString(data))
		req.Header.Set("Origin", "http://box.test")
		req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	if send(0, "hello ", false) != 200 || send(6, "world", true) != 200 {
		t.Fatal("upload failed")
	}
	if b, _ := os.ReadFile(target); string(b) != "hello world" {
		t.Fatalf("uploaded content: %q", b)
	}
	if send(0, "again", true) == 200 {
		t.Fatal("upload replaced an existing file without asking")
	}
	rec := get(h, "/api/files/download?path="+target+"&inline=1", sess)
	if rec.Body.String() != "hello world" || !strings.Contains(rec.Header().Get("Content-Security-Policy"), "sandbox") {
		t.Fatalf("download: %q %v", rec.Body, rec.Header())
	}
	os.WriteFile(filepath.Join(root, "x.svg"), []byte("<svg onload=alert(1)>"), 0o644)
	rec = get(h, "/api/files/download?path="+filepath.Join(root, "x.svg")+"&inline=1", sess)
	if rec.Header().Get("Content-Type") != "application/octet-stream" {
		t.Fatalf("svg served inline: %v", rec.Header())
	}
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func TestFilesCopyZipSizeStayInside(t *testing.T) {
	h, sess, root := filesTestApp(t)
	outside := t.TempDir()
	os.WriteFile(filepath.Join(outside, "secret"), []byte("top secret"), 0o600)
	os.MkdirAll(filepath.Join(root, "dir", "sub"), 0o755)
	os.WriteFile(filepath.Join(root, "dir", "sub", "f.txt"), []byte("hello"), 0o644)
	os.Symlink(outside, filepath.Join(root, "dir", "escape"))

	// Copy keeps links as links, so the secret is never copied in.
	if rec := fpost(h, "/api/files/copy", map[string]string{"path": filepath.Join(root, "dir"), "to": filepath.Join(root, "copy")}, sess); rec.Code != 200 {
		t.Fatalf("copy: %d %s", rec.Code, rec.Body)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "copy", "sub", "f.txt")); string(b) != "hello" {
		t.Fatal("copied file missing")
	}
	if st, err := os.Lstat(filepath.Join(root, "copy", "escape")); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the symlink was not copied as a link")
	}
	for _, c := range []map[string]string{
		{"path": filepath.Join(root, "dir"), "to": filepath.Join(outside, "x")},
		{"path": filepath.Join(outside, "secret"), "to": filepath.Join(root, "s")},
		{"path": filepath.Join(root, "dir", "escape", "secret"), "to": filepath.Join(root, "s")},
		{"path": filepath.Join(root, "dir"), "to": filepath.Join(root, "dir", "sub", "x")},
		{"path": filepath.Join(root, "dir"), "to": filepath.Join(root, "copy")},
	} {
		if rec := fpost(h, "/api/files/copy", c, sess); rec.Code == 200 {
			t.Fatalf("copy %v was allowed", c)
		}
	}

	// Zip: inside works and never contains the linked secret; outside is refused.
	rec := get(h, "/api/files/zip?path="+filepath.Join(root, "dir"), sess)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "f.txt") || strings.Contains(rec.Body.String(), "secret") {
		t.Fatalf("zip: %d (%d bytes)", rec.Code, rec.Body.Len())
	}
	for _, p := range []string{outside, filepath.Join(root, "dir", "escape"), "/etc"} {
		if rec := get(h, "/api/files/zip?path="+p, sess); rec.Code == 200 {
			t.Fatalf("zipped %s", p)
		}
		if rec := get(h, "/api/files/size?path="+p, sess); rec.Code == 200 {
			t.Fatalf("sized %s", p)
		}
	}
	if rec := get(h, "/api/files/size?path="+filepath.Join(root, "dir"), sess); rec.Code != 200 || !strings.Contains(rec.Body.String(), `"files":1`) {
		t.Fatalf("size: %d %s", rec.Code, rec.Body)
	}

	// Folder uploads create each level; an existing folder is fine with exist_ok, a file is not.
	if rec := fpost(h, "/api/files/mkdir", map[string]string{"path": filepath.Join(root, "dir"), "op": "exist_ok"}, sess); rec.Code != 200 {
		t.Fatalf("mkdir exist_ok: %d %s", rec.Code, rec.Body)
	}
	if rec := fpost(h, "/api/files/mkdir", map[string]string{"path": filepath.Join(root, "dir", "sub", "f.txt"), "op": "exist_ok"}, sess); rec.Code == 200 {
		t.Fatal("exist_ok accepted a file")
	}
	if rec := fpost(h, "/api/files/mkdir", map[string]string{"path": filepath.Join(root, "dir", "escape", "new"), "op": "exist_ok"}, sess); rec.Code == 200 {
		t.Fatal("created a folder through a link outside")
	}
}
