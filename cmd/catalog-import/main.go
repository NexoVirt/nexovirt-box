// catalog-import builds Box's signed app catalog from local checkouts of the Runtipi,
// CasaOS and BigBear app stores. It runs offline on the build machine, never on a Box.
//
//	go run ./cmd/catalog-import -runtipi ../stores/runtipi-appstore -casaos ../stores/CasaOS-AppStore \
//	    -bigbear ../stores/big-bear-casaos -out catalog
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/NexoVirt/nexovirt-box/internal/catalog"
	"gopkg.in/yaml.v3"
)

var stores = map[string]catalog.Attribution{
	"runtipi": {Store: "Runtipi App Store", URL: "https://github.com/runtipi/runtipi-appstore", License: "GPL-3.0"},
	"casaos":  {Store: "CasaOS App Store", URL: "https://github.com/IceWhaleTech/CasaOS-AppStore", License: "Apache-2.0"},
	"bigbear": {Store: "BigBearCasaOS", URL: "https://github.com/bigbeartechworld/big-bear-casaos", License: "MIT"},
}

type stats struct {
	imported, duplicate, privileged, broken map[string]int
}

func main() {
	runtipi := flag.String("runtipi", "", "checkout of runtipi/runtipi-appstore")
	casaos := flag.String("casaos", "", "checkout of IceWhaleTech/CasaOS-AppStore")
	bigbear := flag.String("bigbear", "", "checkout of bigbeartechworld/big-bear-casaos")
	out := flag.String("out", "catalog", "output directory")
	keyPath := flag.String("key", os.ExpandEnv("$HOME/.config/nexovirt/box-catalog.key"), "base64 Ed25519 seed (release key, kept outside the repository)")
	allowPriv := flag.Bool("allow-privileged", false, "also import apps that need privileged mode or the host network")
	enrich := flag.Bool("enrich", false, "keep the apps in -out and only refresh first_login and notes (from -casaos/-bigbear), then re-sign")
	fixLayout := flag.Bool("side-by-side", false, "keep the apps in -out, apply the side-by-side mount fixes (layout.go), drop apps that still nest mounts, re-sign")
	flag.Parse()
	if *fixLayout {
		if err := fixLayoutCatalog(*out, *keyPath); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *enrich {
		if err := enrichCatalog(*out, *keyPath, *casaos, *bigbear); err != nil {
			log.Fatal(err)
		}
		return
	}

	st := stats{map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}}
	var apps []*catalog.App
	seen := map[string]string{} // dedupe key -> name of the app that kept it
	var merges []string
	add := func(store string, a *catalog.App, err error) {
		if err != nil {
			if err == errPrivileged {
				st.privileged[store]++
			} else {
				st.broken[store]++
				log.Printf("%s: %v", store, err)
			}
			return
		}
		if a == nil {
			return
		}
		// Duplicates: the same normalized name, or the same main image (for example
		// "Adguard" and "AdGuard Home"). Stores are read in preference order, so the first wins.
		keys := []string{"name:" + normName(a.Name)}
		if img := mainImage(a.Compose); img != "" {
			keys = append(keys, "image:"+img)
		}
		for _, k := range keys {
			if first, ok := seen[k]; ok {
				st.duplicate[store]++
				if first != a.Name {
					merges = append(merges, fmt.Sprintf("%s (%s) -> %s [%s]", a.Name, store, first, k))
				}
				return
			}
		}
		for _, k := range keys {
			seen[k] = a.Name
		}
		a.Attribution = stores[store]
		apps = append(apps, a)
		st.imported[store]++
	}

	if *runtipi != "" {
		dirs, _ := filepath.Glob(filepath.Join(*runtipi, "apps", "*"))
		sort.Strings(dirs)
		for _, d := range dirs {
			a, err := importRuntipi(d, *allowPriv)
			add("runtipi", a, err)
		}
	}
	for _, s := range []struct{ store, root string }{{"casaos", *casaos}, {"bigbear", *bigbear}} {
		if s.root == "" {
			continue
		}
		dirs, _ := filepath.Glob(filepath.Join(s.root, "Apps", "*"))
		sort.Strings(dirs)
		for _, d := range dirs {
			a, err := importCasa(d, *allowPriv)
			add(s.store, a, err)
		}
	}

	// Unique ids.
	used := map[string]bool{}
	for _, a := range apps {
		id := slug(a.Name)
		for n := 2; used[id]; n++ {
			id = fmt.Sprintf("%s-%d", slug(a.Name), n)
		}
		used[id] = true
		a.ID = id
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].ID < apps[j].ID })
	apps = sideBySide(apps) // no bind mounts inside each other (layout.go)
	for _, a := range apps {
		applyFirstLogin(a)
	}

	sort.Strings(merges)
	fmt.Printf("Merged %d near-duplicates:\n", len(merges))
	for _, m := range merges {
		fmt.Println("  " + m)
	}

	if err := write(*out, *keyPath, apps, map[string]string{"runtipi": *runtipi, "casaos": *casaos, "bigbear": *bigbear}); err != nil {
		log.Fatal(err)
	}
	fmt.Printf("catalog: %d apps\n", len(apps))
	for _, s := range []string{"runtipi", "casaos", "bigbear"} {
		fmt.Printf("  %-8s imported %3d  duplicate %3d  privileged/host %2d  unreadable %2d\n", s, st.imported[s], st.duplicate[s], st.privileged[s], st.broken[s])
	}
}

func write(out, keyPath string, apps []*catalog.App, roots map[string]string) error {
	priv, err := catalog.LoadSigningKey(keyPath)
	if err != nil {
		return err
	}
	if err := catalog.WriteDir(out, priv, apps); err != nil {
		return err
	}
	// License texts of the imported catalogs.
	os.MkdirAll(filepath.Join(out, "LICENSES"), 0o755)
	for store, root := range roots {
		if root == "" {
			continue
		}
		if b, err := os.ReadFile(filepath.Join(root, "LICENSE")); err == nil {
			hdr := fmt.Sprintf("Apps imported from %s (%s), license %s.\n\n", stores[store].Store, stores[store].URL, stores[store].License)
			os.WriteFile(filepath.Join(out, "LICENSES", store+".txt"), append([]byte(hdr), b...), 0o644)
		}
	}
	return nil
}

// ---- Runtipi ----

type runtipiConfig struct {
	Name        string   `json:"name"`
	Available   *bool    `json:"available"`
	Port        int      `json:"port"`
	ID          string   `json:"id"`
	Version     string   `json:"version"`
	Categories  []string `json:"categories"`
	ShortDesc   string   `json:"short_desc"`
	Description string   `json:"description"`
	Source      string   `json:"source"`
	Website     string   `json:"website"`
	Arch        []string `json:"supported_architectures"`
	Deprecated  bool     `json:"deprecated"`
	FormFields  []struct {
		Type        string `json:"type"`
		Label       string `json:"label"`
		Hint        string `json:"hint"`
		Placeholder string `json:"placeholder"`
		Default     any    `json:"default"`
		Required    bool   `json:"required"`
		Min         int    `json:"min"`
		Max         int    `json:"max"`
		EnvVariable string `json:"env_variable"`
		Options     []struct {
			Label string `json:"label"`
			Value string `json:"value"`
		} `json:"options"`
	} `json:"form_fields"`
}

func importRuntipi(dir string, allowPriv bool) (*catalog.App, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return nil, nil // not an app folder
	}
	var c runtipiConfig
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("%s: %v", dir, err)
	}
	if (c.Available != nil && !*c.Available) || c.Deprecated || c.Name == "" {
		return nil, nil
	}
	compose, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		return nil, fmt.Errorf("%s: no docker-compose.yml", filepath.Base(dir))
	}
	a := &catalog.App{
		Summary: catalog.Summary{
			Name:          c.Name,
			Version:       c.Version,
			Tagline:       firstNonEmpty(c.ShortDesc, c.Description),
			Icon:          "https://cdn.jsdelivr.net/gh/runtipi/runtipi-appstore@master/apps/" + filepath.Base(dir) + "/metadata/logo.jpg",
			Architectures: c.Arch,
		},
		Description: c.Description,
		Website:     c.Website,
		Source:      c.Source,
		MainPort:    c.Port,
		Scheme:      "http",
	}
	if len(c.Categories) > 0 {
		a.Category = category(c.Categories[0])
	}
	if md, err := os.ReadFile(filepath.Join(dir, "metadata", "description.md")); err == nil {
		a.Description = strings.TrimSpace(string(md))
	}
	for _, f := range c.FormFields {
		if f.EnvVariable == "" {
			continue
		}
		q := catalog.Question{Key: f.EnvVariable, Label: f.Label, Hint: f.Hint, Placeholder: f.Placeholder,
			Required: f.Required, Min: f.Min, Max: f.Max}
		if f.Default != nil {
			q.Default = fmt.Sprint(f.Default)
		}
		switch f.Type {
		case "password":
			q.Type = "password"
		case "random":
			q.Type, q.Generate, q.Required = "password", true, false
			if q.Min == 0 {
				q.Min = 32
			}
		case "number":
			q.Type = "number"
		case "email":
			q.Type = "email"
		case "boolean":
			q.Type = "boolean"
		case "fqdn", "fqdnip":
			q.Type = "domain"
		default:
			q.Type = "text"
		}
		for _, o := range f.Options {
			q.Options = append(q.Options, o.Value)
		}
		if len(q.Options) > 0 {
			q.Type = "select"
		}
		a.Questions = append(a.Questions, q)
	}
	if err := processCompose(a, compose, "runtipi", 0, allowPriv); err != nil {
		return nil, err
	}
	return a, nil
}

// ---- CasaOS / BigBear ----

func importCasa(dir string, allowPriv bool) (*catalog.App, error) {
	compose, err := os.ReadFile(filepath.Join(dir, "docker-compose.yml"))
	if err != nil {
		return nil, nil
	}
	var doc struct {
		X struct {
			Main          string            `yaml:"main"`
			Category      string            `yaml:"category"`
			Architectures []string          `yaml:"architectures"`
			Title         map[string]string `yaml:"title"`
			Tagline       map[string]string `yaml:"tagline"`
			Description   map[string]string `yaml:"description"`
			Icon          string            `yaml:"icon"`
			Screenshots   []string          `yaml:"screenshot_link"`
			PortMap       string            `yaml:"port_map"`
			Scheme        string            `yaml:"scheme"`
			Website       string            `yaml:"website"`
			ProjectURL    string            `yaml:"project_url"`
			Tips          yaml.Node         `yaml:"tips"` // decoded leniently: shapes vary
		} `yaml:"x-casaos"`
		Services map[string]struct {
			Image string `yaml:"image"`
		} `yaml:"services"`
	}
	if err := yaml.Unmarshal(compose, &doc); err != nil {
		return nil, fmt.Errorf("%s: %v", filepath.Base(dir), err)
	}
	name := english(doc.X.Title)
	if name == "" {
		name = filepath.Base(dir)
	}
	a := &catalog.App{
		Summary: catalog.Summary{
			Name:          name,
			Category:      category(doc.X.Category),
			Tagline:       english(doc.X.Tagline),
			Icon:          doc.X.Icon,
			Architectures: doc.X.Architectures,
		},
		Description: strings.TrimSpace(english(doc.X.Description)),
		Screenshots: doc.X.Screenshots,
		Website:     firstNonEmpty(doc.X.Website, doc.X.ProjectURL),
		Scheme:      firstNonEmpty(doc.X.Scheme, "http"),
	}
	if img := doc.Services[doc.X.Main].Image; img != "" {
		if i := strings.LastIndex(img, ":"); i > strings.LastIndex(img, "/") {
			a.Version = img[i+1:]
		} else {
			a.Version = "latest"
		}
	}
	a.MainPort, _ = strconv.Atoi(strings.TrimSpace(doc.X.PortMap))
	var tips struct {
		BeforeInstall map[string]string `yaml:"before_install"`
	}
	if doc.X.Tips.Kind == yaml.MappingNode && doc.X.Tips.Decode(&tips) == nil {
		a.Notes = cleanTip(english(tips.BeforeInstall))
	}
	if err := processCompose(a, compose, "casaos", a.MainPort, allowPriv); err != nil {
		return nil, err
	}
	return a, nil
}

// ---- compose processing (shared) ----

var errPrivileged = fmt.Errorf("needs privileged mode or the host network")

var (
	appDataRe = regexp.MustCompile(`/DATA/AppData/(\$\{?AppID\}?|[A-Za-z0-9._-]+)`)
	appIDRe   = regexp.MustCompile(`\$\{?AppID\}?`)
)

// processCompose cleans a store compose file into the Box form and fills App.Compose,
// DockerSocket, generated-secret questions and the APP_PORT question.
func processCompose(a *catalog.App, src []byte, flavor string, mainPort int, allowPriv bool) error {
	var root yaml.Node
	if err := yaml.Unmarshal(src, &root); err != nil || len(root.Content) == 0 {
		return fmt.Errorf("%s: compose not readable", a.Name)
	}
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: compose not a mapping", a.Name)
	}
	for _, k := range []string{"x-casaos", "version", "name"} {
		delKey(doc, k)
	}
	services := mapGet(doc, "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return fmt.Errorf("%s: no services", a.Name)
	}
	catalog.FixShapesNode(doc) // before the privilege check: `network-mode: host` is host network too
	for i := 1; i < len(services.Content); i += 2 {
		svc := services.Content[i]
		if svc.Kind != yaml.MappingNode {
			continue
		}
		delKey(svc, "x-casaos")
		if !allowPriv {
			if v := mapGet(svc, "privileged"); v != nil && v.Value == "true" {
				return errPrivileged
			}
			for _, k := range []string{"network_mode", "pid"} {
				if v := mapGet(svc, k); v != nil && v.Value == "host" {
					return errPrivileged
				}
			}
		}
		cleanLabels(svc)
		cleanNetworks(svc)
		if dns := mapGet(svc, "dns"); dns != nil && strings.Contains(nodeText(dns), "DNS_IP") {
			delKey(svc, "dns")
		}
		if vols := mapGet(svc, "volumes"); vols != nil && strings.Contains(nodeText(vols), "docker.sock") {
			a.DockerSocket = true
		}
		if flavor == "casaos" && mainPort > 0 {
			rewritePorts(mapGet(svc, "ports"), mainPort)
		}
	}
	if nets := mapGet(doc, "networks"); nets != nil {
		delKey(nets, "tipi_main_network")
		if len(nets.Content) == 0 {
			delKey(doc, "networks")
		}
	}
	if flavor == "casaos" {
		walkScalars(doc, func(n *yaml.Node) {
			v := appDataRe.ReplaceAllString(n.Value, "$${APP_DATA_DIR}")
			v = strings.ReplaceAll(v, "/DATA/", "${SHARED_DIR}/")
			n.Value = appIDRe.ReplaceAllString(v, "$${APP_ID}")
		})
	}
	// A store tip that names a default password Box replaced with a generated one would be wrong.
	for _, lit := range catalog.FixSecretsNode(a, doc) {
		if regexp.MustCompile(`(^|[^A-Za-z0-9])` + regexp.QuoteMeta(lit) + `([^A-Za-z0-9]|$)`).MatchString(a.Notes) {
			a.Notes = ""
		}
	}

	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return err
	}
	a.Compose = buf.String()
	if errs := catalog.ValidateCompose(a.Compose); len(errs) > 0 {
		return fmt.Errorf("%s: compose shape: %s", a.Name, strings.Join(errs, "; "))
	}
	if a.MainPort > 0 && strings.Contains(a.Compose, "APP_PORT") {
		a.Questions = append([]catalog.Question{{Key: "APP_PORT", Label: "Web port", Type: "port",
			Default: strconv.Itoa(a.MainPort), Required: true, Hint: "The port on this server that opens the app."}}, a.Questions...)
	} else if !strings.Contains(a.Compose, "APP_PORT") && flavor == "runtipi" {
		a.MainPort = 0 // the app publishes no web port of its own
	}
	if a.Questions == nil {
		a.Questions = []catalog.Question{}
	}
	return nil
}

func cleanLabels(svc *yaml.Node) {
	labels := mapGet(svc, "labels")
	if labels == nil {
		return
	}
	drop := func(k string) bool { return strings.HasPrefix(k, "traefik.") || strings.HasPrefix(k, "runtipi.") }
	switch labels.Kind {
	case yaml.MappingNode:
		var kept []*yaml.Node
		for i := 0; i+1 < len(labels.Content); i += 2 {
			if !drop(labels.Content[i].Value) {
				kept = append(kept, labels.Content[i], labels.Content[i+1])
			}
		}
		labels.Content = kept
	case yaml.SequenceNode:
		var kept []*yaml.Node
		for _, n := range labels.Content {
			if !drop(n.Value) {
				kept = append(kept, n)
			}
		}
		labels.Content = kept
	}
	if len(labels.Content) == 0 {
		delKey(svc, "labels")
	}
}

func cleanNetworks(svc *yaml.Node) {
	nets := mapGet(svc, "networks")
	if nets == nil {
		return
	}
	switch nets.Kind {
	case yaml.SequenceNode:
		var kept []*yaml.Node
		for _, n := range nets.Content {
			if n.Value != "tipi_main_network" {
				kept = append(kept, n)
			}
		}
		nets.Content = kept
	case yaml.MappingNode:
		delKey(nets, "tipi_main_network")
	}
	if len(nets.Content) == 0 {
		delKey(svc, "networks")
	}
}

// rewritePorts turns the published web port into ${APP_PORT} so the admin can pick it.
func rewritePorts(ports *yaml.Node, mainPort int) {
	if ports == nil || ports.Kind != yaml.SequenceNode {
		return
	}
	mp := strconv.Itoa(mainPort)
	for _, p := range ports.Content {
		switch p.Kind {
		case yaml.ScalarNode:
			parts := strings.Split(p.Value, ":")
			if len(parts) >= 2 && parts[len(parts)-2] == mp {
				parts[len(parts)-2] = "${APP_PORT}"
				p.Value = strings.Join(parts, ":")
				p.Style = yaml.DoubleQuotedStyle
				p.Tag = "!!str"
			}
		case yaml.MappingNode:
			if pub := mapGet(p, "published"); pub != nil && strings.Trim(pub.Value, `"`) == mp {
				pub.Value, pub.Tag, pub.Style = "${APP_PORT}", "!!str", yaml.DoubleQuotedStyle
			}
		}
	}
}

// ---- yaml helpers ----

func mapGet(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func delKey(m *yaml.Node, key string) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

// walkScalars visits value scalars (not mapping keys).
func walkScalars(n *yaml.Node, f func(*yaml.Node)) {
	switch n.Kind {
	case yaml.ScalarNode:
		f(n)
	case yaml.MappingNode:
		for i := 1; i < len(n.Content); i += 2 {
			walkScalars(n.Content[i], f)
		}
	case yaml.SequenceNode, yaml.DocumentNode:
		for _, c := range n.Content {
			walkScalars(c, f)
		}
	}
}

func nodeText(n *yaml.Node) string {
	b, _ := yaml.Marshal(n)
	return string(b)
}

// ---- text helpers ----

func english(m map[string]string) string {
	for _, k := range []string{"en_us", "en_US", "en_GB", "en_gb", "custom"} {
		if v := strings.TrimSpace(m[k]); v != "" {
			return v
		}
	}
	for k, v := range m {
		if strings.HasPrefix(strings.ToLower(k), "en") && strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func category(c string) string {
	c = strings.TrimSpace(strings.ReplaceAll(c, "-", " "))
	if c == "" {
		return "Other"
	}
	switch strings.ToLower(c) {
	case "others", "other", "misc":
		return "Other"
	case "ai":
		return "AI"
	case "development", "developer", "developer tools":
		return "Developer"
	case "network", "networking":
		return "Networking"
	}
	return strings.ToUpper(c[:1]) + strings.ToLower(c[1:])
}

// infraImages are helper services (databases, caches, proxies); they never identify an app.
var infraImages = map[string]bool{"postgres": true, "mysql": true, "mariadb": true, "redis": true, "valkey/valkey": true,
	"mongo": true, "nginx": true, "memcached": true, "busybox": true, "alpine": true, "pgvector/pgvector": true,
	"bitnami/redis": true, "bitnami/postgresql": true, "bitnami/mariadb": true, "tensorchord/pgvecto-rs": true}

// mainImage is the repository of the first non-helper service image, without registry host,
// "library/" and tag, so lscr.io/linuxserver/x:1 and linuxserver/x:latest compare equal.
func mainImage(compose string) string {
	var doc struct {
		Services yaml.Node `yaml:"services"`
	}
	if yaml.Unmarshal([]byte(compose), &doc) != nil || doc.Services.Kind != yaml.MappingNode {
		return ""
	}
	for i := 1; i < len(doc.Services.Content); i += 2 {
		img := mapGet(doc.Services.Content[i], "image")
		if img == nil || img.Value == "" || strings.Contains(img.Value, "$") {
			continue
		}
		repo := strings.ToLower(img.Value)
		if at := strings.Index(repo, "@"); at >= 0 {
			repo = repo[:at]
		}
		if c := strings.LastIndex(repo, ":"); c > strings.LastIndex(repo, "/") {
			repo = repo[:c]
		}
		if first, rest, ok := strings.Cut(repo, "/"); ok && strings.ContainsAny(first, ".:") {
			repo = rest
		}
		repo = strings.TrimPrefix(repo, "library/")
		if !infraImages[repo] {
			return repo
		}
	}
	return ""
}

func normName(s string) string {
	return regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(s), "")
}

func slug(s string) string {
	v := strings.Trim(regexp.MustCompile(`[^a-z0-9]+`).ReplaceAllString(strings.ToLower(s), "-"), "-")
	if v == "" {
		v = "app"
	}
	if len(v) > 50 {
		v = strings.Trim(v[:50], "-")
	}
	return v
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
