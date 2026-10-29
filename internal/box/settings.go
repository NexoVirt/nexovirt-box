package box

import (
	"cmp"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
)

// Built-in addresses for a fresh install. A saved non-empty setting wins; an empty one means "use the default".
const (
	defaultReleaseURL = "https://github.com/NexoVirt/nexovirt-box/releases/latest/download"
	defaultCatalogURL = "https://raw.githubusercontent.com/NexoVirt/nexovirt-box/main/catalog"
)

// releaseURL is the update address Box checks: the saved one, else the default.
func (a *App) releaseURL() string {
	return cmp.Or(a.store.setting("release_url"), defaultReleaseURL)
}

// Settings the admin can change in the UI. Each key has its own validation; unknown keys are refused.
var settingKeys = map[string]func(string) (string, error){
	"catalog_url": httpsURL("The catalog URL"),
	"release_url": func(v string) (string, error) {
		v = strings.TrimRight(strings.TrimSpace(v), "/")
		if v != "" && !releaseURLOK(v) {
			return "", userError{"The update address must be an https:// address."}
		}
		return v, nil
	},
}

func httpsURL(what string) func(string) (string, error) {
	return func(v string) (string, error) {
		v = strings.TrimRight(strings.TrimSpace(v), "/")
		if v == "" {
			return "", nil
		}
		u, err := url.Parse(v)
		if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
			return "", userError{what + " must be an https:// address."}
		}
		return v, nil
	}
}

func (a *App) settingsGet(w http.ResponseWriter, r *http.Request) {
	out := map[string]string{}
	for k := range settingKeys {
		out[k] = a.store.setting(k)
	}
	if out["catalog_url"] == "" {
		out["catalog_url"] = a.catalog.getURL()
	}
	out["release_url"] = a.releaseURL()
	writeJSON(w, http.StatusOK, out)
}

func (a *App) settingsSave(w http.ResponseWriter, r *http.Request) {
	var in map[string]string
	r.Body = http.MaxBytesReader(nil, r.Body, 16<<10)
	if json.NewDecoder(r.Body).Decode(&in) != nil {
		writeError(w, http.StatusBadRequest, "The request could not be read.")
		return
	}
	clean := map[string]string{}
	for k, v := range in {
		check, ok := settingKeys[k]
		if !ok {
			writeError(w, http.StatusUnprocessableEntity, "Unknown setting: "+k)
			return
		}
		cv, err := check(v)
		if err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
		clean[k] = cv
	}
	for k, v := range clean {
		if err := a.store.setSetting(k, v); err != nil {
			log.Printf("settings: %v", err)
			writeError(w, http.StatusInternalServerError, "The settings could not be saved.")
			return
		}
		if k == "catalog_url" {
			a.catalog.setURL(cmp.Or(v, a.catalog.fallback))
		}
		a.audit(r, "settings.change", k)
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
