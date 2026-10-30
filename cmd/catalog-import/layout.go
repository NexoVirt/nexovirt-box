package main

// Box never runs an app whose bind mounts sit inside each other (internal/catalog/layout.go).
// The store definitions below nest them; each fix replaces volume entries so the folders sit
// side by side while the paths inside the containers stay the same, and adds the layout steps
// that move an existing install's data. Any other app that still nests binds is dropped.

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/NexoVirt/nexovirt-box/internal/catalog"
)

type layoutFix struct {
	replace map[string][]string // volume entry as written -> new entries (none = removed)
	layout  []catalog.LayoutStep
}

var sideBySideFixes = map[string]layoutFix{
	// The database moves out of the files folder.
	"bewcloud": {
		replace: map[string][]string{"${APP_DATA_DIR}/data/bewcloud/db:/var/lib/postgresql/data": {"${APP_DATA_DIR}/data/bewcloud-db:/var/lib/postgresql/data"}},
		layout:  []catalog.LayoutStep{{From: "data/bewcloud/db", To: "data/bewcloud-db"}},
	},
	// /downloads and /music are what deemix uses; the extra /media mount held both.
	"deemix": {replace: map[string][]string{"${ROOT_FOLDER_HOST}/media:/media": nil}},
	// Halo's own files go into data/halo, the database stays in data/db.
	"halo": {
		replace: map[string][]string{"${APP_DATA_DIR}:/root/.halo2": {"${APP_DATA_DIR}/halo:/root/.halo2"}},
		layout:  []catalog.LayoutStep{{To: "halo", Except: []string{"db"}}},
	},
	// Temporary downloads stay with the app instead of inside the media folder it mounts.
	"kapowarr": {replace: map[string][]string{"${ROOT_FOLDER_HOST}/media/downloads/kapowarr:/app/temp_downloads": {"${APP_DATA_DIR}/data/temp-downloads:/app/temp_downloads"}}},
	// The image documents /config, /config_deemix, /downloads and /music; /media held them all.
	"lidarr-on-steroids": {replace: map[string][]string{"${ROOT_FOLDER_HOST}/media:/media": nil}},
	// The database moves next to the cache folder.
	"mempool": {
		replace: map[string][]string{"${APP_DATA_DIR}/data/db:/var/lib/mysql": {"${APP_DATA_DIR}/db:/var/lib/mysql"}},
		layout:  []catalog.LayoutStep{{From: "data/db", To: "db"}},
	},
	"pi-hole-updatelists": {
		replace: map[string][]string{"${APP_DATA_DIR}/etc/pihole-updatelists:/etc/pihole-updatelists/": {"${APP_DATA_DIR}/pihole-updatelists:/etc/pihole-updatelists/"}},
		layout:  []catalog.LayoutStep{{From: "etc/pihole-updatelists", To: "pihole-updatelists"}},
	},
	// The backend writes custom logos to data/images and the frontend serves them from
	// public/img: the images folder moves next to data and is mounted at both places.
	"pingvin-share-x": {
		replace: map[string][]string{"${APP_DATA_DIR}/data/images:/opt/app/frontend/public/img": {
			"${APP_DATA_DIR}/images:/opt/app/frontend/public/img", "${APP_DATA_DIR}/images:/opt/app/backend/data/images"}},
		layout: []catalog.LayoutStep{{From: "data/images", To: "images"}},
	},
	// The sandbox's dependencies move next to its config, still filled by the config service.
	"dify": {
		replace: map[string][]string{
			"${APP_DATA_DIR}/data/sandbox/dependencies:/dependencies": {"${APP_DATA_DIR}/data/sandbox-dependencies:/dependencies"},
			"source=${APP_DATA_DIR}/data/sandbox": {"${APP_DATA_DIR}/data/sandbox:/configs/sandbox", "${APP_DATA_DIR}/data/sandbox-dependencies:/configs/sandbox/dependencies"},
		},
		layout: []catalog.LayoutStep{{From: "data/sandbox/dependencies", To: "data/sandbox-dependencies"}},
	},
	// Medusa's config goes into config/, next to Downloads and Media.
	"medusa": {
		replace: map[string][]string{"source=${APP_DATA_DIR}": {"${APP_DATA_DIR}/config:/config"}},
		layout:  []catalog.LayoutStep{{To: "config", Except: []string{"Downloads", "Media"}}},
	},
	// Share the library (media/data) next to the download folders instead of all of media.
	"slskd": {replace: map[string][]string{"${ROOT_FOLDER_HOST}/media/:/shared": {"${ROOT_FOLDER_HOST}/media/data:/shared"}}},
}

var (
	volLineRe    = regexp.MustCompile(`^(\s*-\s*)(['"]?)([^'"#]*?)(['"]?)(\s*#.*)?$`)
	sourceLineRe = regexp.MustCompile(`^(\s*)source:\s*['"]?([^'"#]*?)['"]?\s*$`)
)

// splitBind splits "host:container" at the colon before the container path.
func splitBind(v string) (string, string) {
	i := strings.LastIndex(v, ":/")
	return v[:i], v[i+1:]
}

// replaceVolumes rewrites matching volume entries and keeps everything else of the file as is.
// Short syntax: the key is the whole entry. Long syntax: the key is "source=<path>"; the first
// new entry replaces the source, further ones are added as new bind entries after its target.
func replaceVolumes(compose string, replace map[string][]string) (string, int) {
	lines := strings.Split(compose, "\n")
	var out []string
	n := 0
	var extra []string
	indent := ""
	for _, l := range lines {
		if len(extra) > 0 && strings.HasPrefix(strings.TrimSpace(l), "target:") {
			out = append(out, l)
			for _, e := range extra {
				src, dst := splitBind(e)
				out = append(out, indent[:len(indent)-2]+"- type: bind", indent+"source: "+src, indent+"target: "+dst)
			}
			extra = nil
			continue
		}
		if m := sourceLineRe.FindStringSubmatch(l); m != nil {
			if news, ok := replace["source="+strings.TrimSpace(m[2])]; ok && len(news) > 0 {
				n++
				src, _ := splitBind(news[0])
				out = append(out, m[1]+"source: "+src)
				indent, extra = m[1], news[1:]
				continue
			}
		}
		m := volLineRe.FindStringSubmatch(l)
		if m == nil || m[2] != m[4] {
			out = append(out, l)
			continue
		}
		news, ok := replace[strings.TrimSpace(m[3])]
		if !ok {
			out = append(out, l)
			continue
		}
		n++
		for _, v := range news {
			out = append(out, m[1]+m[2]+v+m[2])
		}
	}
	return strings.Join(out, "\n"), n
}

// layoutVars stand in for Box's values in the static check.
func layoutVars(id string) map[string]string {
	return map[string]string{"APP_DATA_DIR": "/box/apps/" + id + "/data", "ROOT_FOLDER_HOST": "/box/shared", "SHARED_DIR": "/box/shared"}
}

// sideBySide applies the fixes and drops apps whose binds still nest.
func sideBySide(apps []*catalog.App) []*catalog.App {
	var kept []*catalog.App
	for _, a := range apps {
		if fix, ok := sideBySideFixes[a.ID]; ok {
			c, n := replaceVolumes(a.Compose, fix.replace)
			if n != len(fix.replace) {
				log.Printf("%s: the side-by-side fix no longer matches the store definition (%d of %d entries)", a.ID, n, len(fix.replace))
			}
			a.Compose, a.Layout = c, fix.layout
		}
		src, err := catalog.BindSources(a.Compose, layoutVars(a.ID))
		if err != nil {
			log.Printf("%s: dropped, compose not readable: %v", a.ID, err)
			continue
		}
		if pairs := catalog.NestedPairs(src); len(pairs) > 0 {
			log.Printf("%s: dropped, folders mounted inside each other: %v", a.ID, pairs)
			continue
		}
		kept = append(kept, a)
	}
	return kept
}

// fixLayoutCatalog applies sideBySide to the apps in out and re-signs.
func fixLayoutCatalog(out, keyPath string) error {
	priv, err := catalog.LoadSigningKey(keyPath)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(out, "index.json"))
	if err != nil {
		return err
	}
	var idx catalog.Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		return err
	}
	var apps []*catalog.App
	for _, e := range idx.Apps {
		b, err := os.ReadFile(filepath.Join(out, e.File))
		if err != nil {
			return err
		}
		var a catalog.App
		if err := json.Unmarshal(b, &a); err != nil {
			return fmt.Errorf("%s: %w", e.File, err)
		}
		apps = append(apps, &a)
	}
	return catalog.WriteDir(out, priv, sideBySide(apps))
}
