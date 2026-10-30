package box

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestOpenBeneath(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	outside := t.TempDir()
	os.MkdirAll(filepath.Join(root, "a", "b"), 0o755)
	os.Symlink(outside, filepath.Join(root, "a", "link"))
	fd, base, err := openBeneath(root, filepath.Join(root, "a", "b", "file"))
	if err != nil || base != "file" {
		t.Fatal(err)
	}
	unix.Close(fd)
	for _, bad := range []string{
		filepath.Join(root, "a", "link", "file"), // a symlink as an intermediate folder (swapped in after a check)
		filepath.Join(root, "a", "..", "..", "x"),
		root,
		"/etc/passwd",
		"../x",
	} {
		if fd, _, err := openBeneath(root, bad); err == nil {
			unix.Close(fd)
			t.Errorf("%s accepted", bad)
		}
	}
}

func TestRenameNoReplace(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a"), []byte("a"), 0o600)
	os.WriteFile(filepath.Join(dir, "b"), []byte("b"), 0o600)
	fd, _ := unix.Open(dir, dirFlags, 0)
	defer unix.Close(fd)
	if err := renameNoReplace(fd, "a", fd, "b"); !errors.Is(err, unix.EEXIST) {
		t.Fatalf("existing target overwritten: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "b")); string(b) != "b" {
		t.Fatal("target changed")
	}
	if err := renameNoReplace(fd, "a", fd, "c"); err != nil {
		t.Fatal(err)
	}
}
