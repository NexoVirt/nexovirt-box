package box

import (
	"os"
	"path/filepath"
	"testing"
)

// Replacing a file through an upload keeps the old one as a version; restore brings it back and
// keeps the current one as a version too; at most versionsKeep stay; versions are hidden.
func TestFileVersions(t *testing.T) {
	h, root := trashHelper(t)
	doc := filepath.Join(root, "notes.txt")
	upload := func(content string) {
		t.Helper()
		id := "0123456789abcdef"
		os.WriteFile(uploadTemp(doc, id), []byte(content), 0o644)
		if _, err := h.filesUploadFinish(helperRequest{Path: doc, ID: id, Overwrite: true}, nil); err != nil {
			t.Fatal(err)
		}
	}
	upload("v1")
	upload("v2")
	res, _ := h.versionsList(helperRequest{Path: doc}, nil)
	vs := res.(map[string]any)["versions"].([]fileVersion)
	if len(vs) != 1 || vs[0].Size != 2 {
		t.Fatalf("versions %+v", vs)
	}
	if _, err := h.versionRestore(helperRequest{Path: doc, Name: vs[0].ID}, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(doc); string(b) != "v1" {
		t.Fatalf("restored %q", b)
	}
	res, _ = h.versionsList(helperRequest{Path: doc}, nil)
	if vs := res.(map[string]any)["versions"].([]fileVersion); len(vs) != 1 {
		t.Fatalf("the replaced file was not kept: %+v", vs)
	}
	for i := 0; i < versionsKeep+3; i++ {
		upload("x")
	}
	res, _ = h.versionsList(helperRequest{Path: doc}, nil)
	if n := len(res.(map[string]any)["versions"].([]fileVersion)); n != versionsKeep {
		t.Fatalf("%d versions kept", n)
	}
	if _, _, err := h.resolve(filepath.Join(root, versionsDirName), true); err == nil {
		t.Fatal("versions folder reachable")
	}
	if _, err := h.versionRestore(helperRequest{Path: doc, Name: "../../x"}, nil); err == nil {
		t.Fatal("bad id accepted")
	}
}
