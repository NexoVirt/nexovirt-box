package box

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The rollback copy keeps modes and symlinks (as links), and putBack swaps it in completely.
func TestRollbackCopyAndPutBack(t *testing.T) {
	src := filepath.Join(t.TempDir(), "app")
	os.MkdirAll(filepath.Join(src, "data", "db"), 0o750)
	os.WriteFile(filepath.Join(src, ".env"), []byte("A='1'\n"), 0o600)
	os.WriteFile(filepath.Join(src, "data", "db", "x"), []byte("old"), 0o640)
	os.Symlink("/etc/passwd", filepath.Join(src, "data", "link"))
	snap := filepath.Join(t.TempDir(), "snap")
	if err := copyTree(src, snap); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(filepath.Join(snap, ".env")); fi.Mode().Perm() != 0o600 {
		t.Fatalf(".env mode %v", fi.Mode())
	}
	if l, err := os.Readlink(filepath.Join(snap, "data", "link")); err != nil || l != "/etc/passwd" {
		t.Fatalf("symlink not kept as a link: %q %v", l, err)
	}
	if treeSize(snap) != int64(len("A='1'\n")+3) {
		t.Fatalf("size %d", treeSize(snap))
	}
	// The update changes data and adds a file; putBack brings the old state back exactly.
	os.WriteFile(filepath.Join(src, "data", "db", "x"), []byte("new"), 0o640)
	os.WriteFile(filepath.Join(src, "data", "added"), []byte("n"), 0o600)
	if err := putBack(snap, src); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(src, "data", "db", "x")); string(b) != "old" {
		t.Fatalf("data not restored: %q", b)
	}
	if exists(filepath.Join(src, "data", "added")) || exists(src+".rollback-old") {
		t.Fatal("new files or the aside folder are left over")
	}
	// A missing snapshot leaves the current folder untouched.
	if err := putBack(filepath.Join(t.TempDir(), "none"), src); err == nil || !exists(filepath.Join(src, ".env")) {
		t.Fatalf("failed putBack lost the current files: %v", err)
	}
}

func TestAutoUpdateWindow(t *testing.T) {
	at := func(h, m int) time.Time { return time.Date(2026, 1, 1, h, m, 0, 0, time.Local) }
	cases := []struct {
		t        time.Time
		from, to string
		want     bool
	}{
		{at(3, 30), "03:00", "05:00", true},
		{at(5, 0), "03:00", "05:00", false},
		{at(2, 59), "03:00", "05:00", false},
		{at(23, 30), "23:00", "02:00", true},
		{at(1, 0), "23:00", "02:00", true},
		{at(12, 0), "23:00", "02:00", false},
		{at(3, 0), "3:00", "05:00", false},
	}
	for _, c := range cases {
		if got := inWindow(c.t, c.from, c.to); got != c.want {
			t.Errorf("%s in %s-%s = %v", c.t.Format("15:04"), c.from, c.to, got)
		}
	}
}
