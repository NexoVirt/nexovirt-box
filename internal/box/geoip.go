package box

// Sign-in notifications with device and place. The device (browser and system) comes from the
// user agent. The place (city, country) comes from DB-IP's free "IP to City Lite" database
// (CC BY 4.0, https://db-ip.com, attribution shown in Settings), only when the admin turns it
// on: Box then downloads it (about 60 MB, no account or key) into its state folder and refreshes
// it monthly. Lookups are local; no address ever leaves the server.

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/maxminddb-golang/v2"
)

const (
	geoKey      = "geoip_enabled"
	geoMaxBytes = 400 << 20
)

var geoURL = func(t time.Time) string {
	return "https://download.db-ip.com/free/dbip-city-lite-" + t.Format("2006-01") + ".mmdb.gz"
}

type geoDB struct {
	mu      sync.Mutex
	r       *maxminddb.Reader
	loading bool
	err     string
}

func (a *App) geoPath() string { return filepath.Join(a.state, "geoip", "dbip-city-lite.mmdb") }

// deviceOf turns a user agent into "Firefox on Windows".
func deviceOf(ua string) string {
	b := "Unknown browser"
	switch {
	case strings.Contains(ua, "Edg/"):
		b = "Edge"
	case strings.Contains(ua, "OPR/"):
		b = "Opera"
	case strings.Contains(ua, "Firefox/"):
		b = "Firefox"
	case strings.Contains(ua, "Chrome/"):
		b = "Chrome"
	case strings.Contains(ua, "Safari/"):
		b = "Safari"
	case strings.HasPrefix(ua, "curl/"):
		b = "curl"
	case ua != "":
		b = "Browser"
	}
	os := ""
	switch {
	case strings.Contains(ua, "iPhone"), strings.Contains(ua, "iPad"):
		os = "iOS"
	case strings.Contains(ua, "Android"):
		os = "Android"
	case strings.Contains(ua, "Mac OS X"), strings.Contains(ua, "Macintosh"):
		os = "macOS"
	case strings.Contains(ua, "Windows"):
		os = "Windows"
	case strings.Contains(ua, "CrOS"):
		os = "ChromeOS"
	case strings.Contains(ua, "Linux"):
		os = "Linux"
	}
	if os == "" {
		return b
	}
	return b + " on " + os
}

func (a *App) openGeo() {
	r, err := maxminddb.Open(a.geoPath())
	a.geo.mu.Lock()
	defer a.geo.mu.Unlock()
	if err != nil {
		return
	}
	if a.geo.r != nil {
		a.geo.r.Close()
	}
	a.geo.r = r
}

// placeOf is "City, Country" for public addresses when the database is there, else "".
func (a *App) placeOf(ip string) string {
	addr, err := netip.ParseAddr(ip)
	if err != nil || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || a.store.setting(geoKey) != "1" {
		return ""
	}
	a.geo.mu.Lock() // held during the lookup: a refresh must not close the reader under it
	defer a.geo.mu.Unlock()
	r := a.geo.r
	if r == nil {
		return ""
	}
	var rec struct {
		City struct {
			Names map[string]string `maxminddb:"names"`
		} `maxminddb:"city"`
		Country struct {
			Names map[string]string `maxminddb:"names"`
		} `maxminddb:"country"`
	}
	if r.Lookup(addr.Unmap()).Decode(&rec) != nil {
		return ""
	}
	return strings.Trim(rec.City.Names["en"]+", "+rec.Country.Names["en"], ", ")
}

var cleanPlaceRe = regexp.MustCompile(`[\x00-\x1f]`)

// loginDetails is the extra line of a sign-in notification.
func (a *App) loginDetails(ip, agent string) string {
	s := deviceOf(agent)
	if p := cleanPlaceRe.ReplaceAllString(a.placeOf(ip), ""); p != "" {
		s += ", near " + p
	}
	return s
}

// downloadGeo fetches this month's database (or last month's while the new one is not out).
func (a *App) downloadGeo(ctx context.Context) error {
	a.geo.mu.Lock()
	if a.geo.loading {
		a.geo.mu.Unlock()
		return nil
	}
	a.geo.loading, a.geo.err = true, ""
	a.geo.mu.Unlock()
	err := a.fetchGeo(ctx)
	a.geo.mu.Lock()
	a.geo.loading = false
	if err != nil {
		a.geo.err = friendly(err)
	}
	a.geo.mu.Unlock()
	if err == nil {
		a.openGeo()
	}
	return err
}

func (a *App) fetchGeo(ctx context.Context) error {
	var lastErr error
	for _, t := range []time.Time{time.Now(), time.Now().AddDate(0, -1, 0)} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, geoURL(t), nil)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			lastErr = fmt.Errorf("the download answered %d", res.StatusCode)
			continue
		}
		err = a.saveGeo(res.Body)
		res.Body.Close()
		if err == nil {
			return nil
		}
		lastErr = err
	}
	return lastErr
}

func (a *App) saveGeo(body io.Reader) error {
	zr, err := gzip.NewReader(io.LimitReader(body, geoMaxBytes))
	if err != nil {
		return err
	}
	defer zr.Close()
	os.MkdirAll(filepath.Dir(a.geoPath()), 0o700)
	tmp := a.geoPath() + ".part"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	n, err := io.Copy(f, io.LimitReader(zr, geoMaxBytes+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > geoMaxBytes {
		err = fmt.Errorf("the database is too large")
	}
	if err == nil { // must be a database that opens
		var r *maxminddb.Reader
		if r, err = maxminddb.Open(tmp); err == nil {
			r.Close()
		}
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, a.geoPath())
}

// watchGeo opens the database at start and refreshes it monthly while the feature is on.
func (a *App) watchGeo() {
	a.openGeo()
	for {
		if a.store.setting(geoKey) == "1" {
			if st, err := os.Stat(a.geoPath()); err != nil || time.Since(st.ModTime()) > 31*24*time.Hour {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
				if err := a.downloadGeo(ctx); err != nil {
					log.Printf("geoip: %v", err)
				}
				cancel()
			}
		}
		time.Sleep(12 * time.Hour)
	}
}

func (a *App) geoGet(w http.ResponseWriter, r *http.Request) {
	a.geo.mu.Lock()
	st := map[string]any{"enabled": a.store.setting(geoKey) == "1", "ready": a.geo.r != nil, "loading": a.geo.loading, "error": a.geo.err}
	a.geo.mu.Unlock()
	writeJSON(w, http.StatusOK, st)
}

func (a *App) geoSave(w http.ResponseWriter, r *http.Request) {
	var in struct{ Enabled bool }
	if err := readJSON(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	v := "0"
	if in.Enabled {
		v = "1"
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
			defer cancel()
			if _, err := os.Stat(a.geoPath()); err != nil {
				a.downloadGeo(ctx)
			} else {
				a.openGeo()
			}
		}()
	} else {
		os.Remove(a.geoPath())
		a.geo.mu.Lock()
		if a.geo.r != nil {
			a.geo.r.Close()
			a.geo.r = nil
		}
		a.geo.mu.Unlock()
	}
	a.store.setSetting(geoKey, v)
	a.audit(r, "notify.geoip", v)
	a.geoGet(w, r)
}
