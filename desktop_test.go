package main

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
)

func TestDesktopPrefsAndWallpaper(t *testing.T) {
	app, h, token := newTestApp(t)
	app.state = t.TempDir()
	c := sessionFrom(post(h, "/api/setup", `{"token":"`+token+`","username":"admin","password":"long-enough-pw"}`, ""))

	if rec := get(h, "/api/desktop", c); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"wallpaper":"auto"`) {
		t.Fatalf("defaults: %d %s", rec.Code, rec.Body)
	}
	for _, bad := range []string{
		`{"wallpaper":"https://evil"}`,
		`{"wallpaper":"auto","dim":99}`,
		`{"wallpaper":"auto","widgets":[{"id":"x","on":true}]}`,
		`{"wallpaper":"auto","order":["../etc"]}`,
		`{"wallpaper":"custom"}`, // nothing uploaded yet
		`{"wallpaper":"auto","extra":1}`,
	} {
		if rec := post(h, "/api/desktop", bad, c); rec.Code < 400 {
			t.Fatalf("accepted %s", bad)
		}
	}
	if rec := post(h, "/api/desktop", `{"wallpaper":"dunes","dim":20,"blur":4,"widgets":[{"id":"network","on":false}],"order":["sys-files","nextcloud"],"hidden":["sys-backups"]}`, c); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	body := get(h, "/api/desktop", c).Body.String()
	if !strings.Contains(body, `"order":["sys-files","nextcloud"]`) || !strings.Contains(body, `{"id":"clock","on":true}`) {
		t.Fatalf("not saved or widgets not completed: %s", body)
	}

	svg := base64.StdEncoding.EncodeToString([]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	if rec := post(h, "/api/desktop/wallpaper", `{"data":"data:image/svg+xml;base64,`+svg+`"}`, c); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("svg accepted: %d", rec.Code)
	}
	png := base64.StdEncoding.EncodeToString(append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 64)...))
	if rec := post(h, "/api/desktop/wallpaper", `{"data":"data:image/png;base64,`+png+`"}`, c); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"wallpaper":"custom"`) {
		t.Fatalf("png: %d %s", rec.Code, rec.Body)
	}
	if rec := get(h, "/api/desktop/wallpaper", c); rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("serve: %d %s", rec.Code, rec.Header())
	}
	if rec := get(h, "/api/desktop/wallpaper", ""); rec.Code != http.StatusUnauthorized {
		t.Fatalf("no auth: %d", rec.Code)
	}
}
