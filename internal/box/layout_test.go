package box

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NexoVirt/nexovirt-box/internal/catalog"
)

func TestNoNestedBinds(t *testing.T) {
	p, base := testPolicy(t)
	data := filepath.Join(base, "apps", "code-server", "data")
	media := filepath.Join(base, "shared", "media")
	os.MkdirAll(filepath.Join(media, "music"), 0o755)
	os.MkdirAll(filepath.Join(data, "config"), 0o755)
	os.Symlink(filepath.Join(data, "config"), filepath.Join(data, "alias"))
	for name, srcs := range map[string][]string{
		"folder and subfolder":  {data, filepath.Join(data, "db")},
		"folder and file":       {data, filepath.Join(data, "proxy", "nginx.conf")},
		"media and a subfolder": {media, filepath.Join(media, "music")},
		"through a link":        {filepath.Join(data, "alias"), filepath.Join(data, "config", "x")},
	} {
		err := checkMounts(binds(srcs...), p)
		if err == nil || !strings.Contains(err.Error(), "inside") {
			t.Errorf("%s: %v", name, err)
		}
	}
	for name, srcs := range map[string][]string{
		"side by side":        {filepath.Join(data, "config"), filepath.Join(data, "db"), filepath.Join(media, "music")},
		"same folder twice":   {filepath.Join(data, "images"), filepath.Join(data, "images")},
		"media subfolders":    {filepath.Join(media, "music"), filepath.Join(media, "films")},
		"the whole media dir": {media},
	} {
		if err := checkMounts(binds(srcs...), p); err != nil {
			t.Errorf("%s refused: %v", name, err)
		}
	}
	// Other apps: inside the shared folder they may overlap, elsewhere never.
	p.others = map[string][]string{"jellyfin": {media}, "other": {"/srv/films"}}
	if err := checkMounts(binds(filepath.Join(media, "music")), p); err != nil {
		t.Errorf("shared media overlap refused: %v", err)
	}
	for _, src := range []string{"/srv/films/new", "/srv"} {
		if err := checkMounts(binds(src), p); err == nil || !strings.Contains(err.Error(), "other") {
			t.Errorf("%s overlapping another app accepted: %v", src, err)
		}
	}
}

func layoutFixture(t *testing.T) string {
	t.Helper()
	data, _ := filepath.EvalSymlinks(t.TempDir())
	for _, d := range []string{"keys", "db", "etc/updatelists"} {
		os.MkdirAll(filepath.Join(data, d), 0o755)
	}
	os.WriteFile(filepath.Join(data, "halo.yaml"), []byte("x"), 0o644)
	os.WriteFile(filepath.Join(data, "db", "pg"), []byte("db"), 0o644)
	return data
}

func TestApplyLayoutMoves(t *testing.T) {
	data := layoutFixture(t)
	steps := []catalog.LayoutStep{{To: "halo", Except: []string{"db", "etc"}}, {From: "etc/updatelists", To: "updatelists"}}
	if _, err := applyLayout(data, steps); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{"halo/halo.yaml", "halo/keys", "db/pg", "updatelists", "etc"} {
		if _, err := os.Lstat(filepath.Join(data, p)); err != nil {
			t.Errorf("%s missing after the move", p)
		}
	}
	if _, err := os.Lstat(filepath.Join(data, "halo.yaml")); err == nil {
		t.Error("halo.yaml stayed in the data folder")
	}
	// A second run changes nothing.
	if _, err := applyLayout(data, steps); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(data, "halo", "halo.yaml")); err != nil {
		t.Error("second run moved things again")
	}
}

func TestApplyLayoutRefusesLinks(t *testing.T) {
	data := layoutFixture(t)
	outside := t.TempDir()
	os.RemoveAll(filepath.Join(data, "db"))
	os.Symlink(outside, filepath.Join(data, "db")) // planted by the container
	if _, err := applyLayout(data, []catalog.LayoutStep{{To: "halo", Except: []string{"db"}}}); err == nil {
		t.Fatal("a link where a folder should be was accepted")
	}
	if _, err := os.Lstat(filepath.Join(data, "halo.yaml")); err != nil {
		t.Fatal("the old layout was not kept")
	}
	if _, err := os.Lstat(filepath.Join(data, "halo")); err == nil {
		t.Fatal("the new folder was left behind")
	}
	// A link as the folder to move.
	os.RemoveAll(filepath.Join(data, "etc", "updatelists"))
	os.Symlink(outside, filepath.Join(data, "etc", "updatelists"))
	if _, err := applyLayout(data, []catalog.LayoutStep{{From: "etc/updatelists", To: "updatelists"}}); err == nil {
		t.Fatal("moved a link")
	}
}

func TestApplyLayoutFailureKeepsOldLayout(t *testing.T) {
	data := layoutFixture(t)
	os.MkdirAll(filepath.Join(data, "updatelists"), 0o755) // the second step's target is taken
	steps := []catalog.LayoutStep{{To: "halo", Except: []string{"db", "etc", "updatelists"}}, {From: "etc/updatelists", To: "updatelists"}}
	if _, err := applyLayout(data, steps); err == nil {
		t.Fatal("conflict not reported")
	}
	for _, p := range []string{"halo.yaml", "keys", "etc/updatelists"} {
		if _, err := os.Lstat(filepath.Join(data, p)); err != nil {
			t.Errorf("%s not back after the failed move", p)
		}
	}
	if _, err := os.Lstat(filepath.Join(data, "halo")); err == nil {
		t.Error("the new folder was left behind")
	}
}

// fakeDocker puts a docker on PATH that records its calls and answers `compose config` with the
// rendered.json in the project folder.
func fakeDocker(t *testing.T) (calls string) {
	t.Helper()
	bin := t.TempDir()
	calls = filepath.Join(bin, "calls")
	script := "#!/bin/sh\necho \"$@\" >> " + calls + "\ndir=\nwhile [ $# -gt 0 ]; do [ \"$1\" = --project-directory ] && dir=\"$2\"; shift; done\ncat \"$dir/rendered.json\" 2>/dev/null\n"
	os.WriteFile(filepath.Join(bin, "docker"), []byte(script), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return calls
}

func installTestApp(t *testing.T, h *helperServer, id string, meta installedApp, compose, rendered string) string {
	t.Helper()
	dir := h.appDir(id)
	os.MkdirAll(dir, 0o700)
	meta.ID, meta.Name = id, id
	h.writeMeta(meta)
	os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(compose), 0o600)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("APP_ID='"+id+"'\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "rendered.json"), []byte(rendered), 0o600)
	return dir
}

// An own app's compose that fails the raw checks never reaches Docker, not even for a render.
func TestEnsureLayoutChecksBeforeDockerReads(t *testing.T) {
	calls := fakeDocker(t)
	h := &helperServer{appsDir: filepath.Join(t.TempDir(), "apps"), webState: t.TempDir()}
	installTestApp(t, h, "mine", installedApp{Custom: true}, "include:\n  - /etc/evil.yaml\nservices:\n  a:\n    image: x\n", `{}`)
	meta, _ := h.readMeta("mine")
	if _, err := h.ensureLayout(context.Background(), "mine", &meta, func(string) {}); err == nil || !strings.Contains(err.Error(), "include") {
		t.Fatalf("raw check skipped: %v", err)
	}
	if b, _ := os.ReadFile(calls); len(b) > 0 {
		t.Fatalf("Docker read the files first: %s", b)
	}
	// The same app as someone else's neighbour is not rendered either: the check fails closed.
	if _, err := h.otherBinds("other"); err == nil {
		t.Fatal("an app that fails the raw checks was used for the overlap check")
	}
	if b, _ := os.ReadFile(calls); len(b) > 0 {
		t.Fatalf("Docker read the neighbour's files: %s", b)
	}
}

// The other apps' folders come from Docker's rendered config, so defaults and relative paths
// cannot hide an overlap.
func TestOverlapWithOtherAppUsesRenderedConfig(t *testing.T) {
	fakeDocker(t)
	h := &helperServer{appsDir: filepath.Join(t.TempDir(), "apps"), webState: t.TempDir()}
	films := t.TempDir()
	rel := filepath.Join(h.appDir("films"), "library")
	installTestApp(t, h, "films", installedApp{Custom: true},
		"services:\n  a:\n    image: x\n    volumes:\n      - ${MEDIA:-/nowhere}:/m\n      - ./library:/l\n",
		`{"services":{"a":{"volumes":[{"type":"bind","source":"`+films+`"},{"type":"bind","source":"`+rel+`"}]}}}`)
	p := h.policy(installedApp{ID: "new"})
	if p.othersErr != nil {
		t.Fatal(p.othersErr)
	}
	for _, src := range []string{filepath.Join(films, "new"), filepath.Join(rel, "x")} {
		if err := checkMounts(binds(src), p); err == nil || !strings.Contains(err.Error(), "films") {
			t.Errorf("%s overlapping the other app accepted: %v", src, err)
		}
	}
}
