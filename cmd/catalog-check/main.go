// catalog-check resolves every image of every app in the catalog against its registry
// (anonymously, like a Box would pull it), drops apps whose image is missing or private,
// records the architectures the images really ship, replaces literal default secrets with
// generated ones, refuses to sign while any remain, writes a report and re-signs the catalog.
// Run it after catalog-import:
//
//	go run ./cmd/catalog-check -catalog catalog
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/NexoVirt/nexovirt-box/internal/catalog"
	"gopkg.in/yaml.v3"
)

// Architectures Box ships for; the catalog only records these.
var boxArchs = []string{"amd64", "arm64"}

type imageInfo struct {
	Raw    string // as written in the compose file
	Ref    ref
	OK     bool   // false: unresolvable variable or bad reference
	Latest bool   // follows "latest" (explicit or implied), not pinned
	Res    result // filled after checking
}

type appReport struct {
	App    *catalog.App
	Images []*imageInfo
	Drop   string // "", missing, private, unsupported-arch
	Why    string
}

func main() {
	dir := flag.String("catalog", "catalog", "catalog directory (index.json, apps/)")
	cachePath := flag.String("cache", ".catalog-check-cache.json", "result cache, so reruns only check what changed")
	reportPath := flag.String("report", "docs/catalog-image-report.md", "report file")
	keyPath := flag.String("key", os.ExpandEnv("$HOME/.config/nexovirt/box-catalog.key"), "base64 Ed25519 seed (release key, kept outside the repository)")
	workers := flag.Int("concurrency", 8, "parallel registry checks")
	maxAge := flag.Duration("max-age", 7*24*time.Hour, "re-check cached results older than this")
	dry := flag.Bool("dry-run", false, "check and report, but do not rewrite the catalog")
	flag.Parse()

	cat, err := catalog.Load(os.DirFS(*dir), catalog.PublicKeys)
	if err != nil {
		log.Fatal(err)
	}
	var reports []*appReport
	shapeFixed := 0
	unique := map[string]ref{}
	for _, e := range cat.Index.Apps {
		a, err := cat.App(e.ID)
		if err != nil {
			log.Fatalf("%s: %v", e.ID, err)
		}
		if n, err := catalog.FixShapes(a); err != nil {
			log.Fatal(err)
		} else if n > 0 {
			fmt.Printf("shapes: %s: %d shape fix(es) (env syntax, typos, missing depends_on, optional env_file)\n", a.ID, n)
			shapeFixed++
		}
		if lits, err := catalog.FixSecrets(a); err != nil {
			log.Fatal(err)
		} else if len(lits) > 0 {
			fmt.Printf("secrets: %s: %d literal(s) now generated\n", a.ID, len(lits))
		}
		rep := &appReport{App: a}
		for _, raw := range composeImages(a.Compose) {
			im := &imageInfo{Raw: raw}
			if s, ok := resolveVars(raw); ok {
				im.Ref, im.OK = parseRef(s)
				im.Latest = im.OK && im.Ref.digest == "" && im.Ref.tag == "latest"
			}
			if im.OK {
				unique[refKey(im.Ref)] = im.Ref
			}
			rep.Images = append(rep.Images, im)
		}
		reports = append(reports, rep)
	}

	cache := loadCache(*cachePath)
	results := checkAll(unique, cache, *workers, *maxAge, func() { saveCache(*cachePath, cache) })
	saveCache(*cachePath, cache)

	var kept []*catalog.App
	for _, rep := range reports {
		for _, im := range rep.Images {
			if im.OK {
				im.Res = results[refKey(im.Ref)]
			} else {
				im.Res = result{Status: "error", Detail: "uses a variable without a default or is not a valid reference"}
			}
		}
		decide(rep)
		if rep.Drop == "" && catalog.NeedsHost(rep.App.Compose) {
			rep.Drop, rep.Why = "host", "needs privileged mode, the host network or the host PID namespace"
		}
		if rep.Drop == "" {
			kept = append(kept, rep.App)
		}
	}
	// A public literal secret means every Box runs with the same password: never sign that.
	bad := 0
	for _, a := range kept {
		if left, err := catalog.LiteralSecrets(a.Compose); err != nil || len(left) > 0 {
			log.Printf("%s: literal secrets %v %v", a.ID, left, err)
			bad++
		}
	}
	if bad > 0 {
		log.Fatalf("%d apps still carry a literal secret", bad)
	}
	// Docker refuses a compose with the wrong shapes at install time: never sign one.
	for _, a := range kept {
		if errs := catalog.ValidateCompose(a.Compose); len(errs) > 0 {
			log.Printf("%s: compose shape: %s", a.ID, strings.Join(errs, "; "))
			bad++
		}
	}
	if bad > 0 {
		log.Fatalf("%d apps have a compose Docker would refuse", bad)
	}
	if shapeFixed > 0 {
		fmt.Printf("shapes: fixed %d apps\n", shapeFixed)
	}
	if err := os.WriteFile(*reportPath, []byte(render(reports, len(unique))), 0o644); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("checked %d apps (%d images), kept %d, report %s\n", len(reports), len(unique), len(kept), *reportPath)
	if *dry {
		return
	}
	priv, err := catalog.LoadSigningKey(*keyPath)
	if err != nil {
		log.Fatal(err)
	}
	if err := catalog.WriteDir(*dir, priv, kept); err != nil {
		log.Fatal(err)
	}
}

func refKey(r ref) string {
	v := ":" + r.tag
	if r.digest != "" {
		v = "@" + r.digest
	}
	return r.host + "/" + r.repo + v
}

// composeImages lists services.*.image in file order.
func composeImages(compose string) []string {
	var doc struct {
		Services yaml.Node `yaml:"services"`
	}
	if yaml.Unmarshal([]byte(compose), &doc) != nil || doc.Services.Kind != yaml.MappingNode {
		return nil
	}
	var out []string
	for i := 1; i < len(doc.Services.Content); i += 2 {
		svc := doc.Services.Content[i]
		for j := 0; j+1 < len(svc.Content); j += 2 {
			if svc.Content[j].Value == "image" && svc.Content[j+1].Value != "" {
				out = append(out, svc.Content[j+1].Value)
			}
		}
	}
	return out
}

// decide drops apps with a missing or private image and narrows Architectures to what every
// image really ships, never wider than the source store listed. Images that could not be
// checked (errors, rate limits) never drop an app.
func decide(rep *appReport) {
	archSets := [][]string{}
	for _, im := range rep.Images {
		switch im.Res.Status {
		case "missing", "private":
			if rep.Drop != "missing" { // missing wins over private
				rep.Drop, rep.Why = im.Res.Status, im.Raw+": "+im.Res.Detail
			}
		case "ok":
			if len(im.Res.Archs) > 0 {
				archSets = append(archSets, im.Res.Archs)
			}
		}
	}
	if rep.Drop != "" || len(archSets) == 0 {
		return
	}
	var archs []string
	for _, a := range boxArchs {
		all := true
		for _, s := range archSets {
			all = all && slices.Contains(s, a)
		}
		// The source store's own list may be narrower (a binary inside the image, a GPU build):
		// never widen it.
		if all && (len(rep.App.Architectures) == 0 || slices.Contains(rep.App.Architectures, a)) {
			archs = append(archs, a)
		}
	}
	if len(archs) == 0 {
		rep.Drop, rep.Why = "unsupported-arch", "the images ship neither amd64 nor arm64 for every service (or not the ones the store lists)"
		return
	}
	rep.App.Architectures = archs
}

func checkAll(unique map[string]ref, cache map[string]result, workers int, maxAge time.Duration, checkpoint func()) map[string]result {
	c := newChecker()
	out := map[string]result{}
	var todo []string
	for k := range unique {
		if r, ok := cache[k]; ok && r.Status != "error" && r.Status != "rate-limited" && time.Since(time.Unix(r.CheckedAt, 0)) < maxAge {
			out[k] = r
			continue
		}
		todo = append(todo, k)
	}
	sort.Strings(todo)
	fmt.Printf("%d images cached, %d to check\n", len(out), len(todo))
	var mu sync.Mutex
	jobs := make(chan string)
	var wg sync.WaitGroup
	done := 0
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for k := range jobs {
				r := unique[k]
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
				res := c.check(ctx, r)
				cancel()
				mu.Lock()
				out[k], cache[k] = res, res
				if done++; done%25 == 0 {
					fmt.Printf("  %d/%d\n", done, len(todo))
					checkpoint()
				}
				mu.Unlock()
			}
		}()
	}
	for _, k := range todo {
		jobs <- k
	}
	close(jobs)
	wg.Wait()
	return out
}

func loadCache(path string) map[string]result {
	m := map[string]result{}
	if b, err := os.ReadFile(path); err == nil {
		json.Unmarshal(b, &m)
	}
	return m
}

func saveCache(path string, m map[string]result) {
	b, _ := json.MarshalIndent(m, "", " ")
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, path)
	}
}

func render(reports []*appReport, images int) string {
	count := map[string]int{}
	var removed, amdOnly, latest, unchecked []string
	kept := 0
	for _, rep := range reports {
		a := rep.App
		if rep.Drop != "" {
			count[rep.Drop]++
			removed = append(removed, fmt.Sprintf("| %s | %s | %s | %s | %s |", a.ID, a.Name, a.Attribution.Store, rep.Drop, esc(rep.Why)))
			continue
		}
		kept++
		if slices.Equal(a.Architectures, []string{"amd64"}) {
			amdOnly = append(amdOnly, a.ID)
		}
		var lt, uc []string
		for _, im := range rep.Images {
			if im.Latest {
				lt = append(lt, im.Raw)
			}
			if im.Res.Status == "error" || im.Res.Status == "rate-limited" {
				uc = append(uc, fmt.Sprintf("%s (%s: %s)", im.Raw, im.Res.Status, im.Res.Detail))
			}
		}
		if len(lt) > 0 {
			latest = append(latest, fmt.Sprintf("- %s: %s", a.ID, strings.Join(lt, ", ")))
		}
		if len(uc) > 0 {
			unchecked = append(unchecked, fmt.Sprintf("- %s: %s", a.ID, esc(strings.Join(uc, "; "))))
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Catalog image report\n\nGenerated by `go run ./cmd/catalog-check` on %s.\n\n", time.Now().UTC().Format("2006-01-02 15:04 MST"))
	fmt.Fprintf(&b, "| | Count |\n|---|---|\n| Apps checked | %d |\n| Unique images | %d |\n| Kept | %d |\n", len(reports), images, kept)
	for _, k := range []string{"missing", "private", "unsupported-arch"} {
		fmt.Fprintf(&b, "| Removed: %s | %d |\n", k, count[k])
	}
	fmt.Fprintf(&b, "| Kept, amd64 only | %d |\n| Kept, follows `latest` | %d |\n| Kept, image could not be checked | %d |\n\n", len(amdOnly), len(latest), len(unchecked))
	b.WriteString("## Removed\n\n| ID | Name | Store | Reason | Detail |\n|---|---|---|---|---|\n")
	b.WriteString(strings.Join(removed, "\n") + "\n\n")
	b.WriteString("## amd64 only\n\nThese apps have `\"architectures\": [\"amd64\"]`; Box refuses to install them on arm64.\n\n")
	b.WriteString(strings.Join(amdOnly, ", ") + "\n\n")
	b.WriteString("## Following `latest`\n\nKept, but an update can change them without notice.\n\n" + strings.Join(latest, "\n") + "\n\n")
	b.WriteString("## Not checked\n\nKept: the registry could not be asked (errors, rate limits, variables without a default).\n\n" + strings.Join(unchecked, "\n") + "\n")
	return b.String()
}

func esc(s string) string { return strings.ReplaceAll(s, "|", "\\|") }
