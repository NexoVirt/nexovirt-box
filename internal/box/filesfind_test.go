package box

import (
	"os"
	"path/filepath"
	"strings"
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
	secret := filepath.Join(root, "secret")
	vis := func(p string) bool { return p != secret && !strings.HasPrefix(p, secret+"/") }
	all := func(string) bool { return true }
	got := findInRoots([]string{root}, "HOLIDAY", vis)
	if len(got) != 1 || got[0].Path != filepath.Join(root, "Photos", "2026", "holiday.jpg") {
		t.Fatalf("got %+v", got)
	}
	if got := findInRoots([]string{root}, "passwd", all); len(got) != 0 {
		t.Fatalf("followed a symlink out of the root: %+v", got)
	}
	if got := findInRoots([]string{root}, "photos", all); len(got) != 1 || !got[0].Dir {
		t.Fatalf("folder not found: %+v", got)
	}
}

// A root under a blocked folder is not searched at all (browsing it is refused too).
func TestFindSkipsBlockedRoots(t *testing.T) {
	old := firewallDir
	firewallDir = t.TempDir()
	t.Cleanup(func() { firewallDir = old })
	good := t.TempDir()
	os.WriteFile(filepath.Join(good, "report.txt"), nil, 0o644)
	// A configured root inside a blocked folder (/etc) must give nothing.
	writeJSONFile(fwPath("files.json"), filesConfig{Roots: []string{"/etc", good}})
	h := &helperServer{appsDir: t.TempDir()}
	roots, visible := h.walkRoots()
	for _, r := range roots {
		if r == "/etc" || strings.HasPrefix(r, "/private/etc") {
			t.Fatalf("blocked root kept: %v", roots)
		}
	}
	if visible("/etc/passwd") {
		t.Fatal("/etc/passwd is visible")
	}
	res, _ := h.filesFind(helperRequest{Name: "passwd"}, nil)
	if len(res.([]foundFile)) != 0 {
		t.Fatalf("blocked names leaked: %+v", res)
	}
	res, _ = h.filesFind(helperRequest{Name: "report"}, nil)
	if len(res.([]foundFile)) != 1 {
		t.Fatalf("normal root not searched: %+v", res)
	}
}
