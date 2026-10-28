package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type customBody struct {
	Op                string `json:"op"` // add, edit
	ID                string `json:"id"`
	Name              string `json:"name"`
	Compose           string `json:"compose"`
	Env               string `json:"env"`
	Icon              string `json:"icon"`
	AllowDockerSocket bool   `json:"allow_docker_socket"`
}

func (a *App) customAppSave(w http.ResponseWriter, r *http.Request) {
	var in customBody
	r.Body = http.MaxBytesReader(nil, r.Body, maxCompose+maxEnv+16<<10)
	if err := readJSONRaw(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "The request could not be read. The compose file can have at most 256 KB.")
		return
	}
	if in.Op != "add" && in.Op != "edit" {
		writeError(w, http.StatusBadRequest, "This action is not allowed.")
		return
	}
	if in.Op == "add" && in.ID == "" {
		in.ID = slugID(in.Name)
	}
	if validID(in.ID) != nil || in.ID == "box" {
		writeError(w, http.StatusUnprocessableEntity, "Give the app a name with at least one letter or digit.")
		return
	}
	if in.Op == "add" {
		if cat, _ := a.catalog.get(); cat != nil {
			if _, ok := cat.Entry(in.ID); ok {
				writeError(w, http.StatusUnprocessableEntity, "An app in the store already uses this name. Pick another name.")
				return
			}
		}
	}
	if !a.needProof(w, r) {
		return
	}
	tok := sessionToken(r)
	kind := map[string]string{"add": "install", "edit": "edit"}[in.Op]
	a.runJob(w, r, kind, in.ID, "app.custom_"+in.Op, helperRequest{Action: "custom_app_save", Op: in.Op, ID: in.ID, Name: in.Name,
		Compose: in.Compose, Env: in.Env, Icon: in.Icon, AllowDockerSocket: in.AllowDockerSocket, Session: hashToken(tok), Proof: a.consoleProof(tok)})
}

func (a *App) customAppGet(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if validID(id) != nil {
		writeError(w, http.StatusNotFound, "This app does not exist.")
		return
	}
	a.audit(r, "app.custom_view", id)
	a.helperJSON(w, r, helperRequest{Action: "custom_app_get", ID: id})
}

func (a *App) iconDir() string { return filepath.Join(a.state, "app-icons") }

// customIconUpload stores an uploaded icon for an own app (PNG, JPEG, WebP or SVG, 200 KB).
func (a *App) customIconUpload(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if validID(id) != nil {
		writeError(w, http.StatusNotFound, "This app does not exist.")
		return
	}
	var in struct{ Data string }
	r.Body = http.MaxBytesReader(nil, r.Body, 300<<10)
	if err := readJSONRaw(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "The icon can have at most 200 KB.")
		return
	}
	_, b64, _ := strings.Cut(in.Data, ",") // data:image/png;base64,....
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(b) == 0 || len(b) > 200<<10 || !iconOK(b) {
		writeError(w, http.StatusUnprocessableEntity, "Use a PNG, JPEG, WebP or SVG image up to 200 KB.")
		return
	}
	os.MkdirAll(a.iconDir(), 0o700)
	if err := os.WriteFile(filepath.Join(a.iconDir(), id), b, 0o600); err != nil {
		writeError(w, http.StatusInternalServerError, "The icon could not be saved.")
		return
	}
	a.audit(r, "app.icon", id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "v": time.Now().Unix()})
}

func iconOK(b []byte) bool {
	ct := http.DetectContentType(b)
	return ct == "image/png" || ct == "image/jpeg" || ct == "image/webp" || bytes.Contains(b[:min(len(b), 512)], []byte("<svg"))
}

// serveOwnIcon serves an uploaded icon (SVGs sandboxed like catalog media).
func (a *App) serveOwnIcon(w http.ResponseWriter, r *http.Request, id string) bool {
	if validID(id) != nil {
		return false
	}
	b, err := os.ReadFile(filepath.Join(a.iconDir(), id))
	if err != nil || !iconOK(b) {
		return false
	}
	ct := http.DetectContentType(b)
	if !strings.HasPrefix(ct, "image/") {
		ct = "image/svg+xml"
	}
	h := w.Header()
	h.Set("Content-Type", ct)
	h.Set("Cache-Control", "private, max-age=300")
	h.Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
	w.Write(b)
	return true
}

// readJSONRaw decodes the body; the caller sets the size limit.
func readJSONRaw(r *http.Request, v any) error {
	if json.NewDecoder(r.Body).Decode(v) != nil {
		return userError{"The request could not be read."}
	}
	return nil
}
