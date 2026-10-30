package box

// No nested bind mounts. A container that can write a mounted folder could replace a folder
// inside it with a symlink; if another mount uses that inner folder, Docker follows the link
// the next time it starts that container, also through a restart policy before Box can check.
// So the mount check refuses mounts inside each other (within one app, and against other apps
// outside the shared folder), and apps installed with an older nested catalog layout are moved
// to the side-by-side layout (catalog LayoutStep) before they start again. An app that cannot
// be moved safely is stopped and shows "Needs attention: folder layout" with the reason.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NexoVirt/nexovirt-box/internal/catalog"
	"golang.org/x/sys/unix"
)

// nestedText describes the pairs for the admin.
func nestedText(pairs [][2]string) string {
	var parts []string
	for _, p := range pairs {
		parts = append(parts, p[0]+" is inside "+p[1])
	}
	return strings.Join(parts, "; ")
}

func errNested(pairs [][2]string) error {
	return uerr("Folders of this app would be mounted inside each other: %s. Mount them side by side.", nestedText(pairs))
}

// checkNested refuses binds of one app inside each other (paths resolved through links) and,
// outside the shared folder, binds inside another app's binds.
func checkNested(sources []string, p mountPolicy) error {
	shown := map[string]string{} // resolved -> as written
	var real []string
	for _, s := range sources {
		r := resolvePath(s)
		if _, ok := shown[r]; !ok {
			shown[r] = filepath.Clean(s)
		}
		real = append(real, r)
	}
	if pairs := catalog.NestedPairs(real); len(pairs) > 0 {
		for i := range pairs {
			pairs[i] = [2]string{shown[pairs[i][0]], shown[pairs[i][1]]}
		}
		return errNested(pairs)
	}
	shared := resolvePath(sharedDir(p.base))
	inShared := func(x string) bool { return x == shared || catalog.Within(x, shared) }
	for app, theirs := range p.others {
		for _, o := range theirs {
			o = resolvePath(o)
			for _, r := range real {
				if inShared(r) && inShared(o) { // apps share the media folder by design
					continue
				}
				switch {
				case catalog.Within(r, o):
					return uerr("%s is inside %s, which the app %s mounts. Mount them side by side.", shown[r], o, app)
				case catalog.Within(o, r):
					return uerr("%s, which the app %s mounts, is inside %s. Mount them side by side.", o, app, shown[r])
				}
			}
		}
	}
	return nil
}

// envVars reads an app's .env (KEY='value' lines as Box writes them).
func envVars(dir string) map[string]string {
	vars := map[string]string{}
	b, _ := os.ReadFile(filepath.Join(dir, ".env"))
	for _, l := range strings.Split(string(b), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(l), "=")
		if ok && envKeyRe.MatchString(k) {
			vars[k] = strings.Trim(v, `'"`)
		}
	}
	return vars
}

// otherBinds lists the host folders the other installed apps bind (from their compose and .env,
// without Docker).
func (h *helperServer) otherBinds(id string) map[string][]string {
	out := map[string][]string{}
	entries, _ := os.ReadDir(h.appsDir)
	for _, e := range entries {
		if !e.IsDir() || e.Name() == id || validID(e.Name()) != nil {
			continue
		}
		dir := h.appDir(e.Name())
		c, err := os.ReadFile(filepath.Join(dir, "compose.yaml"))
		if err != nil {
			continue
		}
		if src, err := catalog.BindSources(string(c), envVars(dir)); err == nil && len(src) > 0 {
			out[e.Name()] = src
		}
	}
	return out
}

// bindSourcesOf reads the bind sources of a rendered config (`docker compose config` JSON).
func bindSourcesOf(configJSON []byte) []string {
	var cfg struct {
		Services map[string]struct {
			Volumes []struct{ Type, Source string } `json:"volumes"`
		} `json:"services"`
		Volumes map[string]struct {
			DriverOpts map[string]string `json:"driver_opts"`
		} `json:"volumes"`
	}
	json.Unmarshal(configJSON, &cfg)
	var out []string
	for _, s := range cfg.Services {
		for _, v := range s.Volumes {
			switch v.Type {
			case "bind":
				out = append(out, v.Source)
			case "volume":
				if o := cfg.Volumes[v.Source].DriverOpts; o["device"] != "" && (strings.Contains(o["o"], "bind") || o["type"] == "none") {
					out = append(out, o["device"])
				}
			}
		}
	}
	return out
}

func nestedOf(configJSON []byte) [][2]string {
	var real []string
	for _, s := range bindSourcesOf(configJSON) {
		real = append(real, resolvePath(s))
	}
	return catalog.NestedPairs(real)
}

// ---- moving the data of an installed app ----

var errLayoutLink = errors.New("link")

// applyLayout runs the steps inside dataDir. Nothing follows a link: a link where a folder must
// be refuses the move. On failure every move done so far is undone; on success undo puts the
// old layout back (used when the app does not pass its checks afterwards).
func applyLayout(dataDir string, steps []catalog.LayoutStep) (undo func(), err error) {
	if err := catalog.ValidLayout(steps); err != nil {
		return nil, err
	}
	root, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		return func() {}, nil // no data yet: nothing to move
	}
	rfd, err := unix.Open(root, dirFlags, 0)
	if err != nil {
		return nil, uerr("The data folder %s cannot be opened (a link?).", dataDir)
	}
	defer unix.Close(rfd)
	type move struct{ from, to string }
	var done []move
	var made []string
	undo = func() {
		for i := len(done) - 1; i >= 0; i-- {
			if err := moveBeneath(root, done[i].to, done[i].from); err != nil {
				log.Printf("layout undo %s -> %s: %v", done[i].to, done[i].from, err)
			}
		}
		for i := len(made) - 1; i >= 0; i-- {
			unix.Unlinkat(rfd, made[i], unix.AT_REMOVEDIR)
		}
	}
	fail := func(e error) (func(), error) { undo(); return nil, e }
	kind := func(rel string) (uint32, bool, error) { // file type, exists
		fd, name, err := openBeneath(root, rel)
		if err != nil {
			return 0, false, errLayoutLink
		}
		defer unix.Close(fd)
		st, err := lstatAt(fd, name)
		if errors.Is(err, unix.ENOENT) {
			return 0, false, nil
		}
		return uint32(st.Mode) & unix.S_IFMT, err == nil, err
	}
	linkErr := func(rel string) error {
		return uerr("%s in the data folder is a link or a file where a folder should be. Box did not move anything.", rel)
	}
	for _, s := range steps {
		if s.From != "" {
			k, ok, err := kind(s.From)
			if err != nil || (ok && k != unix.S_IFDIR) {
				return fail(linkErr(s.From))
			}
			if !ok {
				continue // never created: nothing to move
			}
			if _, there, err := kind(s.To); err != nil || there {
				return fail(uerr("%s already exists in the data folder. Box did not move anything.", s.To))
			}
			if err := moveBeneath(root, s.From, s.To); err != nil {
				return fail(uerr("%s could not be moved to %s: %v", s.From, s.To, err))
			}
			done = append(done, move{s.From, s.To})
			continue
		}
		keep := map[string]bool{s.To: true}
		for _, e := range s.Except {
			keep[e] = true
			if k, ok, err := kind(e); err != nil || (ok && k != unix.S_IFDIR) {
				return fail(linkErr(e))
			}
		}
		k, ok, err := kind(s.To)
		if err != nil || (ok && k != unix.S_IFDIR) {
			return fail(linkErr(s.To))
		}
		if !ok {
			if err := unix.Mkdirat(rfd, s.To, 0o755); err != nil {
				return fail(err)
			}
			made = append(made, s.To)
			ownLikeParentAt(rfd, s.To)
		}
		names, err := readDirNamesFD(rfd)
		if err != nil {
			return fail(err)
		}
		for _, n := range names {
			if keep[n] {
				continue
			}
			if err := moveBeneath(root, n, s.To+"/"+n); err != nil {
				return fail(uerr("%s could not be moved into %s: %v", n, s.To, err))
			}
			done = append(done, move{n, s.To + "/" + n})
		}
	}
	return undo, nil
}

// moveBeneath renames from to to (both relative to root) without following links or replacing.
func moveBeneath(root, from, to string) error {
	ffd, fname, err := openBeneath(root, from)
	if err != nil {
		return err
	}
	defer unix.Close(ffd)
	tfd, tname, err := openBeneath(root, to)
	if err != nil {
		return err
	}
	defer unix.Close(tfd)
	return renameNoReplace(ffd, fname, tfd, tname)
}

// ---- before an app starts ----

// ensureLayout makes sure an installed app does not mount folders inside each other before it
// starts: a catalog app on an older nested layout is moved to the catalog's side-by-side layout
// (its data moved, compose.yaml and .env updated, the previous files kept as *.layout-prev);
// anything else is stopped and marked. It reports whether it moved the app.
func (h *helperServer) ensureLayout(ctx context.Context, id string, meta *installedApp, logf func(string)) (bool, error) {
	dir := h.appDir(id)
	out, err := renderConfig(ctx, dir, id)
	if err != nil {
		return false, err
	}
	var app *catalog.App
	if !meta.Custom {
		if cat, _, err := loadCatalog(h.webState); err == nil {
			app, _ = cat.App(id)
		}
	}
	pairs := nestedOf(out)
	if len(pairs) == 0 {
		changed := meta.LayoutIssue != ""
		meta.LayoutIssue = ""
		if !meta.Custom && !meta.LayoutOK {
			if app != nil && len(app.Layout) > 0 { // data restored from before the move
				if _, err := applyLayout(meta.DataDir, app.Layout); err != nil {
					return false, h.layoutIssue(ctx, meta, err.Error())
				}
				logf("Moved the data of " + meta.Name + " to the current folder layout.")
			}
			meta.LayoutOK, changed = true, true
		}
		if changed {
			h.writeMeta(*meta)
		}
		return false, nil
	}
	switch {
	case meta.Custom:
		return false, h.layoutIssue(ctx, meta, "Its folders are mounted inside each other ("+nestedText(pairs)+"). Edit the app and mount them side by side.")
	case app == nil:
		return false, h.layoutIssue(ctx, meta, "It mounts folders inside each other ("+nestedText(pairs)+") and is no longer in the catalog. Back up its data and remove it.")
	}
	vars := envVars(dir)
	vars["APP_DATA_DIR"] = meta.DataDir
	if src, err := catalog.BindSources(app.Compose, vars); err != nil || len(catalog.NestedPairs(src)) > 0 {
		return false, h.layoutIssue(ctx, meta, "It mounts folders inside each other ("+nestedText(pairs)+") and the catalog has no side-by-side version of it yet.")
	}
	logf("Moving " + meta.Name + " to the side-by-side folder layout")
	if err := runStream(ctx, compose(dir, id, "stop"), logf); err != nil {
		return false, h.layoutIssue(ctx, meta, "It could not be stopped to move its folders.")
	}
	env, _ := os.ReadFile(filepath.Join(dir, ".env"))
	newEnv, _, err := mergeEnv(env, app)
	if err != nil {
		return false, h.layoutIssue(ctx, meta, err.Error())
	}
	files := []string{"compose.yaml", ".env", "app.json"}
	for _, f := range files {
		if err := copyPrivate(filepath.Join(dir, f), filepath.Join(dir, f+".layout-prev")); err != nil {
			return false, h.layoutIssue(ctx, meta, "Its files could not be copied before the move.")
		}
	}
	putBack := func() {
		for _, f := range files {
			copyPrivate(filepath.Join(dir, f+".layout-prev"), filepath.Join(dir, f))
		}
	}
	undo, err := applyLayout(meta.DataDir, app.Layout)
	if err != nil {
		return false, h.layoutIssue(ctx, meta, err.Error())
	}
	err = os.WriteFile(filepath.Join(dir, "compose.yaml"), []byte(app.Compose), 0o600)
	if err == nil {
		err = os.WriteFile(filepath.Join(dir, ".env"), newEnv, 0o600)
	}
	if err == nil {
		err = checkConfig(ctx, dir, id, h.policy(*meta), false)
	}
	if err != nil {
		undo()
		putBack()
		return false, h.layoutIssue(ctx, meta, "The move was undone: "+friendly(err))
	}
	meta.ComposeSHA, meta.Version, meta.LayoutOK, meta.LayoutIssue = composeSHA([]byte(app.Compose)), app.Version, true, ""
	h.writeMeta(*meta)
	logf("Moved " + meta.Name + " to the side-by-side folder layout. The previous files are kept as *.layout-prev.")
	log.Printf("layout: moved %s to the side-by-side folder layout", id)
	return true, nil
}

// layoutIssue stops the app, records why and returns the error for the action.
func (h *helperServer) layoutIssue(ctx context.Context, meta *installedApp, reason string) error {
	meta.LayoutIssue = reason
	h.writeMeta(*meta)
	runStream(ctx, compose(h.appDir(meta.ID), meta.ID, "stop"), func(string) {})
	log.Printf("layout: %s needs attention: %s", meta.ID, reason)
	return uerr("Needs attention: folder layout. %s", reason)
}

// checkLayouts runs at helper start: every installed app on an older nested layout is moved
// (and started again if it ran), or stopped and marked.
func (h *helperServer) checkLayouts() {
	if needDocker() != nil {
		return
	}
	entries, _ := os.ReadDir(h.appsDir)
	for _, e := range entries {
		id := e.Name()
		if !e.IsDir() || validID(id) != nil {
			continue
		}
		release, err := h.claim(id, "layout")
		if err != nil {
			continue
		}
		func() {
			defer release()
			meta, err := h.readMeta(id)
			if err != nil || exists(filepath.Join(h.appDir(id), installMarker)) {
				return
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
			defer cancel()
			_, state, _ := composeStatus(h.appDir(id), id)
			running := state == "running" || state == "partial" || state == "restarting"
			logf := func(s string) { log.Printf("layout %s: %s", id, s) }
			moved, err := h.ensureLayout(ctx, id, &meta, logf)
			if err != nil || !moved || !running || meta.Stopped {
				return
			}
			if err := runStream(ctx, compose(h.appDir(id), id, "up", "-d", "--remove-orphans"), logf); err != nil {
				log.Printf("layout: %s did not start after the move: %v", id, err)
			}
		}()
	}
}
