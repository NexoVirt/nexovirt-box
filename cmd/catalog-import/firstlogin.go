package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/nexovirt/box/internal/catalog"
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
}

func applyFirstLogin(a *catalog.App) {
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

// enrichCatalog keeps the apps in out as they are and only refreshes first_login and the CasaOS
// tips, so a catalog does not need a full re-import (and image check) for them.
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
		before := fmt.Sprint(a.FirstLogin, a.Notes)
		applyFirstLogin(a)
		if casaos != "" || bigbear != "" {
			a.Notes = notes[a.Attribution.Store+"\x00"+a.Name]
		}
		if fmt.Sprint(a.FirstLogin, a.Notes) != before {
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
