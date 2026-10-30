package box

// File search for the command palette: names containing the query, across the allowed roots.
// Bounded: 30 results, 200000 entries, 4 seconds; blocked folders and symlinks are not followed.

import (
	"errors"
	"io/fs"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	findMaxResults = 30
	findMaxEntries = 200000
	findTimeout    = 4 * time.Second
)

type foundFile struct {
	Path string `json:"path"`
	Dir  bool   `json:"dir"`
}

var errFindDone = errors.New("done")

func findInRoots(roots []string, query string, visible func(string) bool) []foundFile {
	q := strings.ToLower(query)
	out := []foundFile{}
	deadline := time.Now().Add(findTimeout)
	seen := 0
	for _, root := range roots {
		filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return nil
			}
			seen++
			if seen > findMaxEntries || len(out) >= findMaxResults || time.Now().After(deadline) {
				return errFindDone
			}
			if !visible(p) { // the same rule as browsing, the root included
				if d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			if d.IsDir() && strings.HasPrefix(d.Name(), ".nxbox") {
				return filepath.SkipDir
			}
			if p != root && strings.Contains(strings.ToLower(d.Name()), q) {
				out = append(out, foundFile{Path: p, Dir: d.IsDir()})
			}
			return nil
		})
	}
	return out
}

func (h *helperServer) filesFind(req helperRequest, _ func(string)) (any, error) {
	q := strings.TrimSpace(req.Name)
	if utf8.RuneCountInString(q) < 2 || len(q) > 100 || controlChar.MatchString(q) {
		return []foundFile{}, nil
	}
	roots, visible := h.walkRoots()
	return findInRoots(roots, q, visible), nil
}
