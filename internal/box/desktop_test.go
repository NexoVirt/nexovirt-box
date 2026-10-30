package box

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

func TestDesktopClockAndTheme(t *testing.T) {
	app, h, token := newTestApp(t)
	app.state = t.TempDir()
	c := sessionFrom(post(h, "/api/setup", `{"token":"`+token+`","username":"admin","password":"long-enough-pw"}`, ""))

	// A layout saved before the clock existed keeps its order and gets the default clock.
	app.store.setDesktop(1, `{"wallpaper":"sand","order":["sys-files"]}`)
	if body := get(h, "/api/desktop", c).Body.String(); !strings.Contains(body, `"order":["sys-files"]`) || !strings.Contains(body, `"style":"large"`) {
		t.Fatalf("old layout: %s", body)
	}
	theme := `"theme":{"preset":"forest","accent":"#16a34a","neutral":"warm","surface":"glass","radius":"large","density":"compact","scale":110,"saved":[{"name":"Mine","preset":"rose","accent":"","neutral":"cool","surface":"solid","radius":"small","density":"comfortable","scale":100}]}`
	ok := `{"wallpaper":"auto","clock":{"style":"analog","hours":"24","date":"short","greeting":false,"tz2":"America/New_York"},` + theme + `}`
	if rec := post(h, "/api/desktop", ok, c); rec.Code != http.StatusOK {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	for _, bad := range []string{
		strings.Replace(ok, `"analog"`, `"sundial"`, 1),
		strings.Replace(ok, `America/New_York`, `../../etc/passwd`, 1),
		strings.Replace(ok, `#16a34a`, `red;}`, 1),
		strings.Replace(ok, `"scale":110`, `"scale":300`, 1),
		strings.Replace(ok, `"name":"Mine"`, `"name":" "`, 1),
		strings.Replace(ok, `"preset":"forest"`, `"preset":"neon"`, 1),
	} {
		if rec := post(h, "/api/desktop", bad, c); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("accepted (%d): %s", rec.Code, bad)
		}
	}
}

// A new user sees the setup assistant once; someone who used Box before never does.
func TestDesktopOnboarded(t *testing.T) {
	app, h, token := newTestApp(t)
	sess := sessionFrom(post(h, "/api/setup", `{"token":"`+token+`","username":"admin","password":"long-enough-pw"}`, ""))
	if p := app.loadDesktop(1); p.Onboarded != nil {
		t.Fatal("a new user is already onboarded")
	}
	if rec := post(h, "/api/desktop", `{"onboarded":true}`, sess); rec.Code != 200 {
		t.Fatalf("save: %d %s", rec.Code, rec.Body)
	}
	if p := app.loadDesktop(1); p.Onboarded == nil || !*p.Onboarded {
		t.Fatal("not stored")
	}
	app.store.setDesktop(1, `{"wallpaper":"sand"}`) // saved by a version before the assistant
	if p := app.loadDesktop(1); p.Onboarded == nil || !*p.Onboarded {
		t.Fatal("an existing user would see the assistant")
	}
}
