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

// The trash metadata is untrusted: a forged name or path cannot move files outside the place,
// and a planted symlink as the trash or an item folder is refused.
func TestTrashRefusesForgedItems(t *testing.T) {
	h, root := trashHelper(t)
	os.MkdirAll(filepath.Join(root, "docs"), 0o755)
	os.WriteFile(filepath.Join(root, "docs", "a.txt"), []byte("hello"), 0o640)
	res, err := h.filesDelete(helperRequest{Path: filepath.Join(root, "docs", "a.txt")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	id := res.(map[string]any)["trash"].(string)
	trash := filepath.Join(root, trashDirName)
	meta := filepath.Join(trash, id+".json")
	outside := t.TempDir()

	// Forged name: ignored, the real entry name is used.
	os.WriteFile(meta, []byte(`{"id":"`+id+`","path":"`+filepath.Join(root, "docs", "a.txt")+`","name":"../../../`+filepath.Base(outside)+`/pwned"}`), 0o600)
	got, err := h.trashRestore(helperRequest{Path: root, Name: id}, nil)
	if err != nil || got.(map[string]string)["path"] != filepath.Join(root, "docs", "a.txt") {
		t.Fatalf("restore with a forged name: %v %v", got, err)
	}
	if exists(filepath.Join(outside, "pwned")) {
		t.Fatal("file moved outside the place")
	}

	// Forged path: a folder outside the place is refused.
	res, _ = h.filesDelete(helperRequest{Path: filepath.Join(root, "docs", "a.txt")}, nil)
	id = res.(map[string]any)["trash"].(string)
	os.WriteFile(filepath.Join(trash, id+".json"), []byte(`{"id":"`+id+`","path":"`+filepath.Join(outside, "x")+`"}`), 0o600)
	if _, err := h.trashRestore(helperRequest{Path: root, Name: id}, nil); err == nil || exists(filepath.Join(outside, "a.txt")) {
		t.Fatalf("restore outside the place: %v", err)
	}
	os.WriteFile(filepath.Join(trash, id+".json"), []byte(`{"id":"`+id+`","path":"`+root+`/docs/../../`+filepath.Base(outside)+`/a.txt"}`), 0o600)
	if _, err := h.trashRestore(helperRequest{Path: root, Name: id}, nil); err == nil {
		t.Fatal("unclean path accepted")
	}

	// A symlink planted as an item folder.
	os.RemoveAll(filepath.Join(trash, id))
	os.Symlink(outside, filepath.Join(trash, id))
	os.WriteFile(filepath.Join(outside, "victim"), []byte("x"), 0o600)
	if _, err := h.trashDelete(helperRequest{Path: root, Name: id}, nil); err == nil {
		t.Fatal("deleted through a symlinked item folder")
	}
	h.trashPurge(helperRequest{}, nil)
	if !exists(filepath.Join(outside, "victim")) {
		t.Fatal("purge followed a symlinked item folder")
	}

	// A symlink planted as the whole trash, and a trash with a foreign mode.
	os.RemoveAll(trash)
	os.Symlink(outside, trash)
	os.WriteFile(filepath.Join(root, "docs", "b.txt"), []byte("b"), 0o640)
	if _, err := h.filesDelete(helperRequest{Path: filepath.Join(root, "docs", "b.txt")}, nil); err == nil || !exists(filepath.Join(root, "docs", "b.txt")) {
		t.Fatalf("trashed into a symlinked trash: %v", err)
	}
	os.Remove(trash)
	os.Mkdir(trash, 0o777)
	os.Chmod(trash, 0o777)
	if _, err := h.filesDelete(helperRequest{Path: filepath.Join(root, "docs", "b.txt")}, nil); err == nil {
		t.Fatal("trashed into a world-writable trash")
	}
}
