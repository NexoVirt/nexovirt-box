package box

// Storage analysis for the file explorer: what uses the space below a folder (a size tree for
// the treemap) and a duplicate finder (same size first, then a sha256 of the content). Both walk
// with the shared path rule (walkRoots / visibleIn), never follow symlinks, and stop at a time
// and entry limit. Deleting a duplicate goes through the normal delete, so into the trash.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const (
	scanMaxEntries = 2_000_000
	scanTimeout    = 30 * time.Second
	treeChildren   = 40
	treeDepth      = 3
	dupeMinSize    = 1 << 20 // 1 MB: smaller duplicates do not matter for space
	dupeGroupsMax  = 200
	dupeHashBudget = 50 << 30 // bytes read for hashing at most
)

type sizeNode struct {
	Name     string      `json:"name"`
	Path     string      `json:"path"`
	Size     int64       `json:"size"`
	Dir      bool        `json:"dir"`
	Children []*sizeNode `json:"children,omitempty"`
	Others   int64       `json:"others,omitempty"` // size of the children not listed
}

// buildTree sums a folder; nodes deeper than depth are folded into their parent.
func buildTree(root string, visible func(string) bool, deadline time.Time) (*sizeNode, bool) {
	top := &sizeNode{Name: filepath.Base(root), Path: root, Dir: true}
	nodes := map[string]*sizeNode{root: top}
	seen, complete := 0, true
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		seen++
		if seen > scanMaxEntries || time.Now().After(deadline) {
			complete = false
			return filepath.SkipAll
		}
		if !visible(p) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if p == root {
			return nil
		}
		parent := nodes[filepath.Dir(p)]
		depth := strings.Count(strings.TrimPrefix(p, root), "/")
		var size int64
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				size = info.Size()
			}
		}
		if parent != nil && depth <= treeDepth {
			n := &sizeNode{Name: d.Name(), Path: p, Dir: d.IsDir(), Size: size}
			parent.Children = append(parent.Children, n)
			if d.IsDir() {
				nodes[p] = n
			}
		}
		// Add the size to every listed ancestor.
		if size > 0 {
			for dir := filepath.Dir(p); ; dir = filepath.Dir(dir) {
				if n := nodes[dir]; n != nil {
					n.Size += size
				}
				if dir == root || dir == "/" || dir == "." {
					break
				}
			}
		}
		return nil
	})
	trimTree(top)
	return top, complete
}

func trimTree(n *sizeNode) {
	sort.Slice(n.Children, func(i, j int) bool { return n.Children[i].Size > n.Children[j].Size })
	if len(n.Children) > treeChildren {
		for _, c := range n.Children[treeChildren:] {
			n.Others += c.Size
		}
		n.Children = n.Children[:treeChildren]
	}
	for _, c := range n.Children {
		trimTree(c)
	}
}

func (h *helperServer) storageScan(req helperRequest, _ func(string)) (any, error) {
	real, _, err := h.resolve(req.Path, true)
	if err != nil {
		return nil, err
	}
	if st, err := os.Stat(real); err != nil || !st.IsDir() {
		return nil, uerr("Pick a folder to analyze.")
	}
	_, visible := h.walkRoots()
	tree, complete := buildTree(real, visible, time.Now().Add(scanTimeout))
	return map[string]any{"tree": tree, "complete": complete}, nil
}

type dupeGroup struct {
	Size  int64    `json:"size"`
	Hash  string   `json:"hash"`
	Paths []string `json:"paths"`
}

func hashFile(p string) (string, error) {
	f, err := os.OpenFile(p, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// findDupes groups files of the same content (same size, then same sha256). Hard links of one
// file are not duplicates (they use the space once).
func findDupes(ctx context.Context, root string, visible func(string) bool) ([]dupeGroup, bool) {
	bySize := map[int64][]string{}
	inodes := map[uint64]bool{}
	seen, complete := 0, true
	deadline := time.Now().Add(scanTimeout)
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		seen++
		if seen > scanMaxEntries || time.Now().After(deadline) || ctx.Err() != nil {
			complete = false
			return filepath.SkipAll
		}
		if !visible(p) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil || info.Size() < dupeMinSize {
			return nil
		}
		if st, ok := info.Sys().(*syscall.Stat_t); ok {
			if inodes[uint64(st.Ino)] { //nolint:unconvert // Ino type differs per OS
				return nil
			}
			inodes[uint64(st.Ino)] = true //nolint:unconvert
		}
		bySize[info.Size()] = append(bySize[info.Size()], p)
		return nil
	})
	sizes := make([]int64, 0, len(bySize))
	for s, ps := range bySize {
		if len(ps) > 1 {
			sizes = append(sizes, s)
		}
	}
	sort.Slice(sizes, func(i, j int) bool { return sizes[i] > sizes[j] }) // biggest first: most space to win
	var out []dupeGroup
	var read int64
	for _, s := range sizes {
		if ctx.Err() != nil || read > dupeHashBudget || len(out) >= dupeGroupsMax {
			complete = false
			break
		}
		byHash := map[string][]string{}
		for _, p := range bySize[s] {
			sum, err := hashFile(p)
			read += s
			if err == nil {
				byHash[sum] = append(byHash[sum], p)
			}
		}
		for sum, ps := range byHash {
			if len(ps) > 1 {
				sort.Strings(ps)
				out = append(out, dupeGroup{Size: s, Hash: sum, Paths: ps})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Size*int64(len(out[i].Paths)-1) > out[j].Size*int64(len(out[j].Paths)-1)
	})
	return out, complete
}

func (h *helperServer) storageDupes(ctx context.Context, req helperRequest, logf func(string)) (any, error) {
	real, _, err := h.resolve(req.Path, true)
	if err != nil {
		return nil, err
	}
	_, visible := h.walkRoots()
	logf("Looking for files with the same content in " + req.Path)
	groups, complete := findDupes(ctx, real, visible)
	if groups == nil {
		groups = []dupeGroup{}
	}
	return map[string]any{"groups": groups, "complete": complete}, nil
}

// ---- web ----

func (a *App) storageAnalyze(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	raw, err := a.helper.call(ctx, helperRequest{Action: "storage_scan", Path: r.URL.Query().Get("path")}, nil)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, friendly(err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Write(raw)
}

func (a *App) storageDupesStart(w http.ResponseWriter, r *http.Request) {
	var in filesBody
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	a.runJob(w, r, "dupes", "files-dupes", "files.dupes", helperRequest{Action: "storage_dupes", Path: in.Path})
}
