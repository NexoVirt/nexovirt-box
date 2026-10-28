package main

// Share links: a public download link for one file or folder (zip), with an expiry, an optional
// password and an optional download limit. Only the sha256 of the token is stored. The public
// route /s/{token} serves exactly the stored path, and the helper checks it against the allowed
// folders again on every download (a folder swapped for a symlink later is refused). Every hit
// is rate limited per IP; wrong passwords use the strict sign-in limit.

import (
	"database/sql"
	"encoding/json"
	"html/template"
	"io"
	"log"
	"mime"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const maxLinkHours = 90 * 24

var linkTokenRe = regexp.MustCompile(`^[a-f0-9]{64}$`)

type shareLink struct {
	ID           string `json:"id"`
	Path         string `json:"path"`
	Name         string `json:"name"`
	Dir          bool   `json:"dir"`
	Created      int64  `json:"created"`
	Expires      int64  `json:"expires"`
	Password     bool   `json:"password"`
	MaxDownloads int64  `json:"max_downloads"`
	Downloads    int64  `json:"downloads"`
	Revoked      bool   `json:"revoked"`
	hash         string
}

func (a *App) shareLinksList(w http.ResponseWriter, r *http.Request) {
	rows, err := a.store.db.Query(`SELECT id, path, name, is_dir, created_at, expires_at, password_hash != '', max_downloads, downloads, revoked
		FROM share_links WHERE revoked = 0 AND expires_at > ? ORDER BY created_at DESC LIMIT 500`, time.Now().Unix())
	if err != nil {
		fail(w, err)
		return
	}
	defer rows.Close()
	out := []shareLink{}
	for rows.Next() {
		var l shareLink
		if rows.Scan(&l.ID, &l.Path, &l.Name, &l.Dir, &l.Created, &l.Expires, &l.Password, &l.MaxDownloads, &l.Downloads, &l.Revoked) == nil {
			out = append(out, l)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (a *App) shareLinkCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path         string `json:"path"`
		ExpiresHours int64  `json:"expires_hours"`
		Password     string `json:"password"`
		MaxDownloads int64  `json:"max_downloads"`
	}
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if in.ExpiresHours < 1 || in.ExpiresHours > maxLinkHours {
		writeError(w, http.StatusUnprocessableEntity, "Choose an expiry between 1 hour and 90 days.")
		return
	}
	if in.MaxDownloads < 0 || in.MaxDownloads > 10000 {
		writeError(w, http.StatusUnprocessableEntity, "The download limit must be between 1 and 10000, or none.")
		return
	}
	if in.Password != "" && (len(in.Password) < 6 || len(in.Password) > 72) {
		writeError(w, http.StatusUnprocessableEntity, "The password needs 6 to 72 characters.")
		return
	}
	raw, err := a.helper.call(r.Context(), helperRequest{Action: "files_stat", Path: in.Path}, nil)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	var st struct {
		Dir  bool   `json:"dir"`
		Name string `json:"name"`
	}
	json.Unmarshal(raw, &st)
	pwHash := ""
	if in.Password != "" {
		b, err := bcrypt.GenerateFromPassword([]byte(in.Password), bcrypt.DefaultCost)
		if err != nil {
			fail(w, err)
			return
		}
		pwHash = string(b)
	}
	token, id, now := randomToken(), randomToken()[:8], time.Now()
	l := shareLink{ID: id, Path: in.Path, Name: st.Name, Dir: st.Dir, Created: now.Unix(),
		Expires: now.Add(time.Duration(in.ExpiresHours) * time.Hour).Unix(), Password: pwHash != "", MaxDownloads: in.MaxDownloads}
	if _, err := a.store.db.Exec(`INSERT INTO share_links (id, token_hash, path, name, is_dir, created_at, expires_at, password_hash, max_downloads)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, l.ID, hashToken(token), l.Path, l.Name, l.Dir, l.Created, l.Expires, pwHash, l.MaxDownloads); err != nil {
		fail(w, err)
		return
	}
	a.audit(r, "share_link.create", l.Path)
	scheme := "https"
	if !a.secure {
		scheme = "http"
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": l.ID, "link": l, "url": scheme + "://" + r.Host + "/s/" + token})
}

func (a *App) shareLinkRevoke(w http.ResponseWriter, r *http.Request) {
	res, err := a.store.db.Exec(`UPDATE share_links SET revoked = 1 WHERE id = ?`, r.PathValue("id"))
	if err != nil {
		fail(w, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		writeError(w, http.StatusNotFound, "This link no longer exists.")
		return
	}
	a.audit(r, "share_link.revoke", r.PathValue("id"))
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// ---- public side ----

// liveLink finds a link that still works (not revoked, not expired, downloads left).
func (a *App) liveLink(token string) (shareLink, bool) {
	var l shareLink
	if !linkTokenRe.MatchString(token) {
		return l, false
	}
	err := a.store.db.QueryRow(`SELECT id, path, name, is_dir, expires_at, password_hash, max_downloads, downloads FROM share_links
		WHERE token_hash = ? AND revoked = 0 AND expires_at > ? AND (max_downloads = 0 OR downloads < max_downloads)`,
		hashToken(token), time.Now().Unix()).Scan(&l.ID, &l.Path, &l.Name, &l.Dir, &l.Expires, &l.hash, &l.MaxDownloads, &l.Downloads)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("share link: %v", err)
		}
		return l, false
	}
	l.Password = l.hash != ""
	return l, true
}

func (a *App) sharePublic(w http.ResponseWriter, r *http.Request) {
	ip := a.clientIP(r)
	if !a.shareLimit.allow("share:" + ip) {
		sharePage(w, http.StatusTooManyRequests, sharePageData{Error: "Too many requests. Wait a minute and try again."})
		return
	}
	l, ok := a.liveLink(r.PathValue("token"))
	if !ok {
		sharePage(w, http.StatusNotFound, sharePageData{Error: "This link has expired or was removed."})
		return
	}
	if r.Method == http.MethodPost {
		release, ok := a.limiter.reserve("sharepw:" + ip)
		if !ok {
			sharePage(w, http.StatusTooManyRequests, sharePageData{Link: l, Error: "Too many wrong passwords. Wait a minute and try again."})
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 4096)
		pw := r.PostFormValue("password")
		if !l.Password || bcrypt.CompareHashAndPassword([]byte(l.hash), []byte(pw)) != nil {
			a.store.audit("", ip, "share_link.password_failed", l.ID)
			sharePage(w, http.StatusUnauthorized, sharePageData{Link: l, Error: "The password is wrong."})
			return
		}
		release()
		a.shareServe(w, r, l, ip)
		return
	}
	if r.URL.Query().Get("download") == "1" && !l.Password {
		a.shareServe(w, r, l, ip)
		return
	}
	sharePage(w, http.StatusOK, sharePageData{Link: l})
}

func (a *App) shareServe(w http.ResponseWriter, r *http.Request, l shareLink, ip string) {
	res, err := a.store.db.Exec(`UPDATE share_links SET downloads = downloads + 1 WHERE id = ? AND revoked = 0 AND expires_at > ?
		AND (max_downloads = 0 OR downloads < max_downloads)`, l.ID, time.Now().Unix())
	if n, _ := res.RowsAffected(); err != nil || n == 0 {
		sharePage(w, http.StatusNotFound, sharePageData{Error: "This link has expired or was removed."})
		return
	}
	req := helperRequest{Action: "files_open_read", Path: l.Path}
	if l.Dir {
		req = helperRequest{Action: "files_zip", Path: l.Path}
	}
	raw, f, err := a.helper.openFile(req)
	if err != nil {
		a.store.db.Exec(`UPDATE share_links SET downloads = downloads - 1 WHERE id = ?`, l.ID)
		log.Printf("share link %s: %v", l.ID, err)
		sharePage(w, http.StatusNotFound, sharePageData{Error: "The shared item is no longer available."})
		return
	}
	defer f.Close()
	var meta struct {
		Size int64
		Name string
	}
	json.Unmarshal(raw, &meta)
	a.store.audit("", ip, "share_link.download", l.Path)
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("X-Robots-Tag", "noindex")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": meta.Name}))
	if l.Dir {
		h.Set("Content-Type", "application/zip")
	} else {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Length", strconv.FormatInt(meta.Size, 10))
	}
	io.Copy(w, f)
}

type sharePageData struct {
	Link  shareLink
	Error string
}

func (d sharePageData) Expires() string {
	return time.Unix(d.Link.Expires, 0).UTC().Format("2 Jan 2006, 15:04 UTC")
}

func sharePage(w http.ResponseWriter, status int, d sharePageData) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Robots-Tag", "noindex")
	// The password form posts back here; with no-referrer browsers would send "Origin: null".
	h.Set("Referrer-Policy", "same-origin")
	w.WriteHeader(status)
	if err := sharePageTpl.Execute(w, d); err != nil {
		log.Printf("share page: %v", err)
	}
}

var sharePageTpl = template.Must(template.New("share").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<meta name="robots" content="noindex"><title>{{if .Link.Name}}{{.Link.Name}}{{else}}Shared file{{end}}</title>
<style>
:root{color-scheme:light dark;--bg:#f4f4f5;--surface:#fff;--text:#09090b;--text-2:#52525b;--fill:rgba(113,113,122,.12);--accent:#2563eb;--on:#fff;--error:#dc2626}
@media (prefers-color-scheme:dark){:root{--bg:#09090b;--surface:#18181b;--text:#fafafa;--text-2:#a1a1aa;--fill:rgba(161,161,170,.14);--accent:#60a5fa;--on:#09090b;--error:#f87171}}
*{box-sizing:border-box}body{margin:0;min-height:100svh;display:grid;place-items:center;padding:1rem;background:var(--bg);color:var(--text);font:15px/1.45 -apple-system,BlinkMacSystemFont,"SF Pro Text","Segoe UI",system-ui,sans-serif;-webkit-font-smoothing:antialiased}
main{width:min(26rem,100%);background:var(--surface);border-radius:1.5rem;padding:2rem 1.75rem;display:grid;gap:1rem;box-shadow:0 2px 6px rgba(0,0,0,.05),0 18px 48px rgba(0,0,0,.1)}
h1{margin:0;font-size:1.375rem;line-height:1.2;letter-spacing:-.017em;word-break:break-word}p{margin:0;color:var(--text-2);font-size:.875rem}
.err{color:var(--error);font-weight:500}input{width:100%;height:2.75rem;padding:0 .875rem;border:0;border-radius:.75rem;background:var(--fill);font:inherit;color:inherit}
input:focus-visible{outline:3px solid color-mix(in srgb,var(--accent) 45%,transparent)}
.btn{display:flex;align-items:center;justify-content:center;width:100%;min-height:2.75rem;border:0;border-radius:999px;background:var(--accent);color:var(--on);font:inherit;font-weight:600;text-decoration:none;cursor:pointer}
.btn:active{transform:scale(.97)}.foot{font-size:.75rem}
</style></head><body><main>
{{if .Link.ID}}
<h1>{{.Link.Name}}{{if .Link.Dir}}.zip{{end}}</h1>
<p>{{if .Link.Dir}}A folder, downloaded as a zip file.{{else}}A shared file.{{end}} The link works until {{.Expires}}.</p>
{{if .Error}}<p class="err" role="alert">{{.Error}}</p>{{end}}
{{if .Link.Password}}
<form method="post"><label><p>This link is protected with a password.</p><br><input type="password" name="password" autocomplete="current-password" required autofocus aria-label="Password"></label><br><br><button class="btn">Download</button></form>
{{else}}<a class="btn" href="?download=1" rel="nofollow">Download</a>{{end}}
{{else}}
<h1>Link not available</h1><p class="err">{{.Error}}</p>
{{end}}
<p class="foot">Shared with NexoVirt Box</p>
</main></body></html>`))
