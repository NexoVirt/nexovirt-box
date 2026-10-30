package box

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func trashHelper(t *testing.T) (*helperServer, string) {
	old := firewallDir
	firewallDir = t.TempDir()
	t.Cleanup(func() { firewallDir = old })
	root := t.TempDir()
	root, _ = filepath.EvalSymlinks(root)
	writeJSONFile(fwPath("files.json"), filesConfig{Roots: []string{root}})
	return &helperServer{appsDir: t.TempDir()}, root
}

// Delete moves into the trash, the trash is invisible to browsing and search, restore puts the
// item back (beside it when the name is taken again), and purge removes old items.
func TestTrashFlow(t *testing.T) {
	h, root := trashHelper(t)
	os.MkdirAll(filepath.Join(root, "docs"), 0o755)
	os.WriteFile(filepath.Join(root, "docs", "a.txt"), []byte("hello"), 0o640)
	res, err := h.filesDelete(helperRequest{Path: filepath.Join(root, "docs", "a.txt")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := res.(map[string]any)["trash"].(string)
	if exists(filepath.Join(root, "docs", "a.txt")) {
		t.Fatal("file still there")
	}
	list, _ := h.filesList(helperRequest{Path: root}, nil)
	for _, e := range list.(map[string]any)["entries"].([]fileEntry) {
		if e.Name == trashDirName {
			t.Fatal("trash folder listed")
		}
	}
	if _, _, err := h.resolve(filepath.Join(root, trashDirName, id, "a.txt"), true); err == nil {
		t.Fatal("trash reachable through resolve")
	}
	if got, _ := h.filesFind(helperRequest{Name: "a.txt"}, nil); len(got.([]foundFile)) != 0 {
		t.Fatalf("search found trashed file: %v", got)
	}
	items, _ := h.trashList(helperRequest{}, nil)
	its := items.(map[string]any)["items"].([]trashItem)
	if len(its) != 1 || its[0].Path != filepath.Join(root, "docs", "a.txt") || its[0].Size != 5 {
		t.Fatalf("items %+v", its)
	}
	os.WriteFile(filepath.Join(root, "docs", "a.txt"), []byte("new"), 0o640) // name taken again
	got, err := h.trashRestore(helperRequest{Path: root, Name: id}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if p := got.(map[string]string)["path"]; !strings.HasSuffix(p, "a (restored).txt") {
		t.Fatalf("restored to %s", p)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "docs", "a (restored).txt")); string(b) != "hello" {
		t.Fatal("content lost")
	}
	// Permanent delete and purge.
	h.filesDelete(helperRequest{Path: filepath.Join(root, "docs", "a.txt"), Op: "permanent"}, nil)
	if exists(filepath.Join(root, "docs", "a.txt")) {
		t.Fatal("permanent delete kept the file")
	}
	h.filesDelete(helperRequest{Path: filepath.Join(root, "docs")}, nil)
	if n, _ := h.trashPurge(helperRequest{Offset: 30}, nil); n.(map[string]int)["deleted"] != 0 {
		t.Fatal("fresh item purged")
	}
	if n, _ := h.trashPurge(helperRequest{}, nil); n.(map[string]int)["deleted"] != 1 {
		t.Fatal("empty trash missed the folder")
	}
	if _, err := h.trashRestore(helperRequest{Path: root, Name: "../../etc"}, nil); err == nil {
		t.Fatal("bad id accepted")
	}
}
