package box

// One safe path primitive for root file operations below an allowed root: openBeneath walks
// from the root's real path one component at a time with openat(O_NOFOLLOW|O_DIRECTORY), so no
// symlink (also one swapped into an intermediate folder after a check) and no ".." can lead
// out of the root. Moves then use renameat on the returned descriptors, and a move that must
// not overwrite uses renameNoReplace (renameat2 RENAME_NOREPLACE on Linux).

import (
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

var errEscapes = userError{"This path leaves the allowed folder. Box refused it."}

// openBeneath opens the parent folder of rel (a path below rootReal) and returns its descriptor
// and the base name. rel may be given absolute (inside rootReal) or relative.
func openBeneath(rootReal, path string) (int, string, error) {
	rel := path
	if filepath.IsAbs(path) {
		r, err := filepath.Rel(rootReal, path)
		if err != nil {
			return -1, "", errEscapes
		}
		rel = r
	}
	rel = filepath.ToSlash(rel)
	if rel == "." || rel == "" || strings.HasPrefix(rel, "../") || rel == ".." || filepath.IsAbs(rel) {
		return -1, "", errEscapes
	}
	parts := strings.Split(rel, "/")
	for _, p := range parts {
		if p == "" || p == "." || p == ".." {
			return -1, "", errEscapes
		}
	}
	fd, err := unix.Open(rootReal, dirFlags, 0)
	if err != nil {
		return -1, "", uerr("The folder cannot be opened.")
	}
	for _, p := range parts[:len(parts)-1] {
		next, err := unix.Openat(fd, p, dirFlags, 0)
		unix.Close(fd)
		if err != nil {
			return -1, "", errEscapes // a link, not a folder, or gone
		}
		fd = next
	}
	return fd, parts[len(parts)-1], nil
}
