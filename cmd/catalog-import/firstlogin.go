package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/NexoVirt/nexovirt-box/internal/catalog"
)

// knownFirstLogin is how to sign in to an app the first time, written by hand and only where
// it is certain (checked against the app's docs or a real install). Keyed by catalog id.
var knownFirstLogin = map[string]catalog.FirstLogin{
	"alist": {Username: "admin", PasswordFromLog: `initial password is: (\S+)`,
		Note: "AList creates the admin user on its first start and prints a random password once. Change it after signing in (Profile)."},
	"openlist":    {Username: "admin", PasswordFromEnv: "SECRET_OPENLIST_ADMIN_PASSWORD"},
	"portainer":   {Note: "Open Portainer within 5 minutes after the install and create the admin user. After that Portainer locks the setup for security; restart the app in My Apps to get another 5 minutes."},
	"nextcloud":   {Note: "Sign in with the admin user and password shown here. The first start takes a minute or two while Nextcloud sets up its database."},
	"jellyfin":    {Note: "The first time you open Jellyfin a setup wizard asks you to create the admin account and add media libraries. Your media goes into the shared folder media/data on this server, which Jellyfin sees as /media/data."},
	"vaultwarden": {Note: "Create your own account on the start page with Create account. The admin page is at /admin; sign in there with the admin token, which is the Vaultwarden admin password shown here."},
	"uptime-kuma": {Note: "Uptime Kuma asks you to create the admin account the first time you open it."},

	// Generated on the first start and written to a file in the app's data folder.
	"crafty": {Username: "admin", PasswordFromFile: "data/config/default-creds.txt", PasswordFromFileRegex: `"password"\s*:\s*"([^"]+)"`,
		Note: "Crafty makes a random admin password on its first start; it shows here a moment after that. Crafty answers only on HTTPS (https:// in front of the address) with its own certificate, so your browser asks you to accept it once. Change the password in Crafty after signing in (the file is not updated)."},
	// Printed in the logs; a new one on every start until you set your own.
	"qbittorrent": {Username: "admin", PasswordFromLog: `temporary password is provided for this session: (\S+)`,
		Note: "qBittorrent prints a temporary password on every start until you set your own in Tools, Options, Web UI. If this one does not work, restart the app and open Login details again."},

	// The password is a generated secret in the app's settings; the user name is fixed in the compose file.
	"davis":                     {Username: "bigbear", PasswordFromEnv: "SECRET_ADMIN_PASSWORD", Note: "This signs in to the Davis admin dashboard."},
	"dockpeek":                  {Username: "bigbear", PasswordFromEnv: "SECRET_PASSWORD"},
	"etherpad":                  {Username: "admin", PasswordFromEnv: "SECRET_ADMIN_PASSWORD", Note: "Pads open without signing in. This user signs in to the admin page at /admin."},
	"healthchecks":              {Username: "user@example.com", PasswordFromEnv: "SECRET_SUPERUSER_PASSWORD"},
	"kopia":                     {Username: "casaos", PasswordFromEnv: "SECRET_KOPIA_UI_PASSWORD", Note: "The same password also protects Kopia's repository."},
	"microbin":                  {Username: "admin", PasswordFromEnv: "SECRET_MICROBIN_ADMIN_PASSWORD", Note: "This signs in to the admin page at /admin."},
	"mineos":                    {Username: "mc", PasswordFromEnv: "SECRET_USER_PASSWORD"},
	"nextcloud-with-smb-legacy": {Username: "casaos", PasswordFromEnv: "SECRET_POSTGRES_PASSWORD"},
	"nzbget":                    {Username: "nzbget", PasswordFromEnv: "SECRET_NZBGET_PASS"},
	"seafile-legacy":            {Username: "admin@example.com", PasswordFromEnv: "SECRET_SEAFILE_ADMIN_PASSWORD"},
	"snapotter":                 {Username: "admin", PasswordFromEnv: "SECRET_DEFAULT_PASSWORD"},
	"stirling-pdf":              {Username: "bigbear", PasswordFromEnv: "SECRET_SECURITY_INITIALLOGIN_PASSWORD"},
	"taskingai":                 {Username: "admin", PasswordFromEnv: "SECRET_DEFAULT_ADMIN_PASSWORD"},
	"tududi":                    {Username: "admin@example.com", PasswordFromEnv: "SECRET_TUDUDI_USER_PASSWORD"},
	"webdav":                    {Username: "casaos", PasswordFromEnv: "SECRET_PASSWORD"},

	// Fixed defaults the app ships with (from the app's docs); change them after signing in.
	"alist-sync":               {Username: "admin", Password: "admin"},
	"bookstack":                {Username: "admin@admin.com", Password: "password"},
	"brave":                    {Username: "kasm_user", Password: "casaos"},
	"calibre-web-ebook-reader": {Username: "admin", Password: "admin123"},
	"chrome":                   {Username: "kasm_user", Password: "casaos"},
	"copyparty":                {Username: "casaos", Password: "casaos"},
	"deluge":                   {Password: "deluge", Note: "The Deluge web page only asks for the password."},
	"filebrowser-quantum":      {Username: "admin", Password: "admin"},
	"grafana":                  {Username: "admin", Password: "admin", Note: "Grafana asks for a new password right after the first sign-in."},
	"grocy":                    {Username: "admin", Password: "admin"},
	"kanboard":                 {Username: "admin", Password: "admin"},
	"mixpost":                  {Username: "admin@example.com", Password: "changeme"},
	"mixpost-pro":              {Username: "admin@example.com", Password: "changeme"},
	"peppermint":               {Username: "admin@admin.com", Password: "1234"},
	"solidping":                {Username: "admin@solidping.io", Password: "solidpass", Note: "SolidPing asks for a new password right after the first sign-in. The organization is \"default\"."},
	"speedtest-tracker":        {Username: "admin@example.com", Password: "password"},
	"umami-analytics":          {Username: "admin", Password: "umami"},
}

// knownHTTPS are apps whose web UI answers only on HTTPS (with their own certificate) although
// their store says http.
var knownHTTPS = map[string]bool{"crafty": true}

func applyFirstLogin(a *catalog.App) {
	if knownHTTPS[a.ID] {
		a.Scheme = "https"
	}
	if fl, ok := knownFirstLogin[a.ID]; ok {
		fl := fl
		a.FirstLogin = &fl
	} else {
		a.FirstLogin = nil
	}
}

var (
	// CasaOS tips point at CasaOS menus, which Box does not have.
	casaMenuRe = regexp.MustCompile(`\s*\((?:[^()]*?(?:Terminal and Logs|Top ?Right|TopRight)[^()]*)\)`)
	casaHostRe = regexp.MustCompile(`\b(?:ZimaOS|CasaOS) IP\b`)
)

// cleanTip turns a CasaOS before_install tip into Box wording.
func cleanTip(s string) string {
	s = casaMenuRe.ReplaceAllString(s, " (My Apps, Logs)")
	s = casaHostRe.ReplaceAllString(s, "Server IP")
	return strings.TrimSpace(s)
}

// enrichCatalog keeps the apps in out as they are and only refreshes first_login, the curated
// scheme fixes and the CasaOS tips, so a catalog does not need a full re-import (and image check) for them.
func enrichCatalog(out, keyPath, casaos, bigbear string) error {
	cat, err := catalog.Load(os.DirFS(out), catalog.PublicKeys)
	if err != nil {
		return err
	}
	notes := map[string]string{} // store + name -> tip
	for _, s := range []struct{ store, root string }{{"casaos", casaos}, {"bigbear", bigbear}} {
		if s.root == "" {
			continue
		}
		dirs, _ := filepath.Glob(filepath.Join(s.root, "Apps", "*"))
		for _, d := range dirs {
			if a, err := importCasa(d, true); err == nil && a != nil && a.Notes != "" {
				notes[stores[s.store].Store+"\x00"+a.Name] = a.Notes
			}
		}
	}
	var apps []*catalog.App
	changed := 0
	for _, e := range cat.Index.Apps {
		a, err := cat.App(e.ID)
		if err != nil {
			return fmt.Errorf("%s: %w", e.ID, err)
		}
		before := fmt.Sprint(a.FirstLogin, a.Notes, a.Scheme)
		applyFirstLogin(a)
		if casaos != "" || bigbear != "" {
			a.Notes = notes[a.Attribution.Store+"\x00"+a.Name]
		}
		if fmt.Sprint(a.FirstLogin, a.Notes, a.Scheme) != before {
			changed++
		}
		apps = append(apps, a)
	}
	sort.Slice(apps, func(i, j int) bool { return apps[i].ID < apps[j].ID })
	priv, err := catalog.LoadSigningKey(keyPath)
	if err != nil {
		return err
	}
	if err := catalog.WriteDir(out, priv, apps); err != nil {
		return err
	}
	fmt.Printf("catalog: %d apps, %d changed\n", len(apps), changed)
	return nil
}
