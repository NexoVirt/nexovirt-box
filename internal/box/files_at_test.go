package box

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// A folder swapped for a link after the path check (an app container can do that in its data
// folder) is refused by the descriptor walk, and copies keep links as links.
func TestFileOpsNeverFollowSwappedLinks(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	outside, _ := filepath.EvalSymlinks(t.TempDir())
	os.WriteFile(filepath.Join(outside, "secret"), []byte("s"), 0o600)
	r := rootInfo{root, root}
	os.Mkdir(filepath.Join(root, "dir"), 0o755)
	checked := filepath.Join(root, "dir", "secret") // what resolve saw
	os.Remove(filepath.Join(root, "dir"))
	os.Symlink(outside, filepath.Join(root, "dir")) // swapped afterwards
	if fd, _, err := beneath(r, checked); err == nil {
		unix.Close(fd)
		t.Fatal("walked through a swapped link")
	}

	os.Mkdir(filepath.Join(root, "src"), 0o755)
	os.Symlink(filepath.Join(outside, "secret"), filepath.Join(root, "src", "link"))
	syscall.Mkfifo(filepath.Join(root, "src", "pipe"), 0o644)
	fd, _ := unix.Open(root, dirFlags, 0)
	defer unix.Close(fd)
	if err := copyAt(fd, "src", fd, "dst"); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Lstat(filepath.Join(root, "dst", "link")); err != nil || st.Mode()&os.ModeSymlink == 0 {
		t.Fatal("the link was not copied as a link")
	}
	if _, err := os.Lstat(filepath.Join(root, "dst", "pipe")); err == nil {
		t.Fatal("a pipe was copied")
	}
	sfd, _ := unix.Open(filepath.Join(root, "src"), dirFlags, 0)
	defer unix.Close(sfd)
	if f, err := openFileAt(sfd, "pipe"); err == nil {
		f.Close()
		t.Fatal("a pipe was opened as a file")
	}
	if f, err := openFileAt(sfd, "link"); err == nil {
		f.Close()
		t.Fatal("a link was followed")
	}
}
