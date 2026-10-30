package catalog

// Bind mounts of one app never sit inside each other. A container that can write the outer
// folder could replace the inner one with a symlink, and Docker follows that symlink the next
// time it starts the other container (also through a restart policy, before Box can check).
// NestedPairs finds such pairs; BindSources reads the host folders of a catalog compose file
// without Docker (for the catalog checks); LayoutStep moves the data of an installed app from
// an old nested layout to the side-by-side one.

import (
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// LayoutStep is one move inside the app's data folder (APP_DATA_DIR), run once when an app
// installed with an older nested layout moves to the current one. With From, that folder moves
// to To. Without From, every entry of the data folder except Except (and To itself) moves into
// the new folder To. Paths are relative to the data folder.
type LayoutStep struct {
	From   string   `json:"from,omitempty"`
	To     string   `json:"to"`
	Except []string `json:"except,omitempty"`
}

func relOK(p string) bool {
	return p != "" && p != "." && !path.IsAbs(p) && path.Clean(p) == p && p != ".." && !strings.HasPrefix(p, "../")
}

// ValidLayout checks the steps of a catalog app.
func ValidLayout(steps []LayoutStep) error {
	for i, s := range steps {
		if !relOK(s.To) || (s.From != "" && (!relOK(s.From) || s.From == s.To || len(s.Except) > 0)) {
			return fmt.Errorf("layout step %d is not valid", i+1)
		}
		for _, e := range s.Except {
			if !relOK(e) || strings.Contains(e, "/") {
				return fmt.Errorf("layout step %d: %q is not a name in the data folder", i+1, e)
			}
		}
		if s.From == "" && strings.Contains(s.To, "/") {
			return fmt.Errorf("layout step %d: gather into a folder directly in the data folder", i+1)
		}
	}
	return nil
}

// NestedPairs returns every pair [inner, outer] where inner lies below outer. Equal paths are
// fine (the same folder mounted twice is never a mount point inside another mount).
func NestedPairs(paths []string) [][2]string {
	seen := map[string]bool{}
	var ps []string
	for _, p := range paths {
		p = path.Clean(p)
		if !seen[p] {
			seen[p] = true
			ps = append(ps, p)
		}
	}
	sort.Strings(ps)
	var out [][2]string
	for _, outer := range ps {
		for _, inner := range ps {
			if Within(inner, outer) {
				out = append(out, [2]string{inner, outer})
			}
		}
	}
	return out
}

// Within reports whether p lies strictly below dir.
func Within(p, dir string) bool {
	return dir != p && strings.HasPrefix(p, strings.TrimSuffix(dir, "/")+"/")
}

var varRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)(?:(:?-)((?:[^{}]|\{[^{}]*\})*))?\}|\$([A-Za-z_][A-Za-z0-9_]*)`)

// interpolate replaces ${VAR}, ${VAR:-default}, ${VAR-default} and $VAR. Unknown variables
// without a default stay as they are.
func interpolate(s string, vars map[string]string) string {
	for range 4 { // defaults can hold variables themselves
		next := varRe.ReplaceAllStringFunc(s, func(m string) string {
			g := varRe.FindStringSubmatch(m)
			name := g[1] + g[4]
			if v, ok := vars[name]; ok && (v != "" || g[2] != ":-") {
				return v
			}
			if g[2] != "" {
				return g[3]
			}
			return m
		})
		if next == s {
			break
		}
		s = next
	}
	return s
}

// BindSources returns the host paths a compose file binds (short and long volume syntax and
// local volumes bound to a device), with vars filled in. Named volumes and paths that stay
// relative or unresolved are left out.
func BindSources(compose string, vars map[string]string) ([]string, error) {
	var doc struct {
		Services map[string]struct {
			Volumes []yaml.Node `yaml:"volumes"`
		} `yaml:"services"`
		Volumes map[string]*struct {
			DriverOpts map[string]string `yaml:"driver_opts"`
		} `yaml:"volumes"`
	}
	if err := yaml.Unmarshal([]byte(compose), &doc); err != nil {
		return nil, err
	}
	var out []string
	add := func(src string) {
		src = interpolate(strings.TrimSpace(src), vars)
		if strings.HasPrefix(src, "/") {
			out = append(out, path.Clean(src))
		}
	}
	for _, s := range doc.Services {
		for _, v := range s.Volumes {
			switch v.Kind {
			case yaml.ScalarNode:
				src := v.Value
				if i := strings.Index(interpolate(src, vars), ":"); i >= 0 {
					src = interpolate(src, vars)[:i]
				}
				add(src)
			case yaml.MappingNode:
				var lv struct{ Type, Source string }
				if v.Decode(&lv) == nil && (lv.Type == "bind" || lv.Type == "") {
					add(lv.Source)
				}
			}
		}
	}
	for _, v := range doc.Volumes {
		if v != nil && v.DriverOpts["device"] != "" && (strings.Contains(v.DriverOpts["o"], "bind") || v.DriverOpts["type"] == "none") {
			add(v.DriverOpts["device"])
		}
	}
	return out, nil
}
