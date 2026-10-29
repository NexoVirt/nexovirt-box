package box

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDefaultURLsUntilSaved(t *testing.T) {
	app, _, _ := newTestApp(t)
	app.catalog = newCatalogStore(t.TempDir(), defaultCatalogURL) // as run.go does without -catalog-url

	read := func() map[string]string {
		rec := httptest.NewRecorder()
		app.settingsGet(rec, httptest.NewRequest(http.MethodGet, "/api/settings", nil))
		var out map[string]string
		json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	save := func(body string) {
		rec := httptest.NewRecorder()
		app.settingsSave(rec, httptest.NewRequest(http.MethodPost, "/api/settings", strings.NewReader(body)))
		if rec.Code != http.StatusOK {
			t.Fatalf("save %s: %d %s", body, rec.Code, rec.Body)
		}
	}

	if s := read(); s["release_url"] != defaultReleaseURL || s["catalog_url"] != defaultCatalogURL {
		t.Fatalf("fresh install: %v", s)
	}
	save(`{"release_url":"https://mirror.example/box","catalog_url":"https://mirror.example/catalog"}`)
	if s := read(); s["release_url"] != "https://mirror.example/box" || s["catalog_url"] != "https://mirror.example/catalog" || app.catalog.getURL() != "https://mirror.example/catalog" {
		t.Fatalf("saved values must win: %v", s)
	}
	save(`{"release_url":"","catalog_url":""}`)
	if s := read(); s["release_url"] != defaultReleaseURL || s["catalog_url"] != defaultCatalogURL || app.catalog.getURL() != defaultCatalogURL || app.releaseURL() != defaultReleaseURL {
		t.Fatalf("empty means default: %v", s)
	}
}
