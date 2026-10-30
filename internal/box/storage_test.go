package box

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStorageTreeAndDupes(t *testing.T) {
	root, _ := filepath.EvalSymlinks(t.TempDir())
	mk := func(p string, size int, fill byte) {
		os.MkdirAll(filepath.Dir(filepath.Join(root, p)), 0o755)
		os.WriteFile(filepath.Join(root, p), bytes.Repeat([]byte{fill}, size), 0o644)
	}
	mk("films/a.mkv", 3<<20, 'a')
	mk("films/copy/a.mkv", 3<<20, 'a') // duplicate
	mk("films/b.mkv", 3<<20, 'b')      // same size, other content
	mk("music/s.mp3", 2<<20, 's')
	mk("small/x.txt", 10, 'x')
	mk(".nxbox-trash/old/a.mkv", 3<<20, 'a') // never counted
	os.Link(filepath.Join(root, "music/s.mp3"), filepath.Join(root, "music/hardlink.mp3"))
	os.Symlink("/", filepath.Join(root, "rootlink"))
	visible := func(p string) bool { return !inTrash(p) }

	tree, complete := buildTree(root, visible, time.Now().Add(time.Minute))
	if !complete || tree.Children[0].Name != "films" || tree.Children[0].Size != 9<<20 {
		t.Fatalf("tree %+v", tree.Children[0])
	}
	for _, c := range tree.Children {
		if c.Name == trashDirName || c.Name == "rootlink" && c.Size > 0 {
			t.Fatalf("counted %s", c.Name)
		}
	}
	groups, _ := findDupes(t.Context(), root, visible)
	if len(groups) != 1 || len(groups[0].Paths) != 2 || !strings.HasSuffix(groups[0].Paths[0], "films/a.mkv") {
		t.Fatalf("dupes %+v", groups)
	}
}
