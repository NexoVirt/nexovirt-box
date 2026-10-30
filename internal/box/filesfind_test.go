package box

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFindInRoots(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "Photos", "2026"), 0o755)
	os.WriteFile(filepath.Join(root, "Photos", "2026", "holiday.jpg"), nil, 0o644)
	os.WriteFile(filepath.Join(root, "notes.txt"), nil, 0o644)
	os.MkdirAll(filepath.Join(root, "secret"), 0o755)
	os.WriteFile(filepath.Join(root, "secret", "holiday-plan.txt"), nil, 0o644)
	os.Symlink("/etc", filepath.Join(root, "etc-link"))
	got := findInRoots([]string{root}, "HOLIDAY", []string{filepath.Join(root, "secret")})
	if len(got) != 1 || got[0].Path != filepath.Join(root, "Photos", "2026", "holiday.jpg") {
		t.Fatalf("got %+v", got)
	}
	if got := findInRoots([]string{root}, "passwd", nil); len(got) != 0 {
		t.Fatalf("followed a symlink out of the root: %+v", got)
	}
	if got := findInRoots([]string{root}, "photos", nil); len(got) != 1 || !got[0].Dir {
		t.Fatalf("folder not found: %+v", got)
	}
}
