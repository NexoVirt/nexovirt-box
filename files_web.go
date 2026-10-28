package main

import (
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const uploadChunk = 8 << 20 // the browser sends uploads in pieces of at most 8 MiB

func (a *App) filesCall(w http.ResponseWriter, r *http.Request, req helperRequest, audit string) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	raw, err := a.helper.call(ctx, req, nil)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	if audit != "" {
		detail := req.Path
		if req.Path2 != "" {
			detail += " -> " + req.Path2
		}
		a.audit(r, audit, detail)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.Write(raw)
}

func (a *App) filesList(w http.ResponseWriter, r *http.Request) {
	a.filesCall(w, r, helperRequest{Action: "files_list", Path: r.URL.Query().Get("path")}, "")
}

type filesBody struct {
	Path  string `json:"path"`
	To    string `json:"to"`
	Roots string `json:"roots"`
	Op    string `json:"op"`
}

func (a *App) filesMutate(action, audit string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var in filesBody
		if err := readJSON(r, &in); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		op := ""
		if in.Op == "exist_ok" {
			op = in.Op
		}
		a.filesCall(w, r, helperRequest{Action: action, Path: in.Path, Path2: in.To, Op: op}, audit)
	}
}

func (a *App) filesRootsSet(w http.ResponseWriter, r *http.Request) {
	var in filesBody
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.filesCall(w, r, helperRequest{Action: "files_roots_set", Path: in.Roots}, "files.roots")
}

// previewTypes may be shown inline. Everything else (HTML, SVG, scripts) is a download, and
// inline answers carry a sandbox CSP, so a file can never run code in Box's origin.
var previewTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".webp": "image/webp",
	".txt": "text/plain; charset=utf-8", ".log": "text/plain; charset=utf-8", ".md": "text/plain; charset=utf-8",
	".json": "text/plain; charset=utf-8", ".yaml": "text/plain; charset=utf-8", ".yml": "text/plain; charset=utf-8",
	".conf": "text/plain; charset=utf-8", ".env": "text/plain; charset=utf-8", ".sh": "text/plain; charset=utf-8",
	".ini": "text/plain; charset=utf-8", ".toml": "text/plain; charset=utf-8", ".csv": "text/plain; charset=utf-8",
}

func (a *App) filesDownload(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Query().Get("path")
	raw, f, err := a.helper.openFile(helperRequest{Action: "files_open_read", Path: path})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	defer f.Close()
	var meta struct {
		Size  int64
		Name  string
		Mtime int64
	}
	json.Unmarshal(raw, &meta)
	ext := strings.ToLower(filepath.Ext(meta.Name))
	ctype, inline := previewTypes[ext]
	inline = inline && r.URL.Query().Get("inline") == "1"
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	if inline {
		h.Set("Content-Type", ctype)
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self'")
		h.Set("Content-Disposition", mime.FormatMediaType("inline", map[string]string{"filename": meta.Name}))
	} else {
		h.Set("Content-Type", "application/octet-stream")
		h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": meta.Name}))
		a.audit(r, "files.download", path)
	}
	h.Set("Content-Length", strconv.FormatInt(meta.Size, 10))
	io.Copy(w, f)
}

// filesUpload takes one chunk: ?path=<target>&id=<16 hex>&offset=N[&final=1&overwrite=1].
// The helper opens the temp file; the last chunk renames it into place.
func (a *App) filesUpload(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	path, id := q.Get("path"), q.Get("id")
	offset, err := strconv.ParseInt(q.Get("offset"), 10, 64)
	if err != nil || offset < 0 {
		writeError(w, http.StatusBadRequest, "The upload is not valid.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, uploadChunk+1)
	chunk, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusRequestEntityTooLarge, "A piece of the upload was too large.")
		return
	}
	if q.Get("abort") == "1" {
		a.filesCall(w, r, helperRequest{Action: "files_finish", Path: path, ID: id, Op: "abort"}, "")
		return
	}
	_, f, err := a.helper.openFile(helperRequest{Action: "files_open_write", Path: path, ID: id, Offset: offset})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	_, werr := f.WriteAt(chunk, offset)
	f.Close()
	if werr != nil {
		writeError(w, http.StatusInsufficientStorage, "The file could not be written. Is the disk full?")
		return
	}
	if q.Get("final") != "1" {
		writeJSON(w, http.StatusOK, map[string]any{"received": offset + int64(len(chunk))})
		return
	}
	a.filesCall(w, r, helperRequest{Action: "files_finish", Path: path, ID: id, Overwrite: q.Get("overwrite") == "1"},
		"files.upload")
}

func (a *App) filesSize(w http.ResponseWriter, r *http.Request) {
	a.filesCall(w, r, helperRequest{Action: "files_size", Path: r.URL.Query().Get("path")}, "")
}

// filesZip streams several files or folders as one zip: ?path=a&path=b.
func (a *App) filesZip(w http.ResponseWriter, r *http.Request) {
	paths := r.URL.Query()["path"]
	raw, f, err := a.helper.openFile(helperRequest{Action: "files_zip", Path: strings.Join(paths, "\n")})
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	defer f.Close()
	var meta struct{ Name string }
	json.Unmarshal(raw, &meta)
	a.audit(r, "files.download", strings.Join(paths, ", "))
	h := w.Header()
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Type", "application/zip")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": meta.Name}))
	io.Copy(w, f)
}
