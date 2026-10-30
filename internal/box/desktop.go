package box

import (
	"encoding/base64"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// Desktop layout per user: wallpaper, widgets and the order of the tiles on the desktop.
// Stored as JSON in users.desktop; every field is checked here before it is saved.

type widgetPref struct {
	ID string `json:"id"`
	On bool   `json:"on"`
	X  *int   `json:"x,omitempty"` // placed freely on the desktop (px from the left), nil = in the column
	Y  *int   `json:"y,omitempty"`
}

// weatherPrefs is the place of the weather widget (Open-Meteo, no API key; widgets.go).
type weatherPrefs struct {
	Name string  `json:"name"`
	Lat  float64 `json:"lat"`
	Lon  float64 `json:"lon"`
}

type clockPrefs struct {
	Style    string `json:"style"`    // large, seconds, analog, minimal
	Hours    string `json:"hours"`    // auto, 12, 24
	Date     string `json:"date"`     // long, short, numeric, off
	Greeting bool   `json:"greeting"` // greeting line under the date
	TZ2      string `json:"tz2"`      // optional second time zone (IANA name), "" = off
}

type desktopPrefs struct {
	Wallpaper string       `json:"wallpaper"`
	Dim       int          `json:"dim"`  // 0..80 % darkening over the wallpaper
	Blur      int          `json:"blur"` // 0..40 px
	Widgets   []widgetPref `json:"widgets"`
	Order     []string     `json:"order"`  // tile ids: app ids and sys-* tiles
	Hidden    []string     `json:"hidden"` // hidden system tiles
	Clock     clockPrefs   `json:"clock"`
	Weather   weatherPrefs `json:"weather"`
	Theme     *themePrefs  `json:"theme,omitempty"`
	CustomV   int64        `json:"custom_v"`
	Onboarded *bool        `json:"onboarded,omitempty"` // the setup assistant was finished or skipped
}

var (
	wallpapers = map[string]bool{"auto": true, "graphite": true, "midnight": true, "mist": true, "sand": true, "forest": true, "dunes": true, "hills": true, "custom": true}
	widgetIDs  = map[string]bool{"clock": true, "system": true, "storage": true, "network": true, "disks": true, "apps": true, "weather": true}
	tileID     = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
	tzName     = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_+-]*(/[A-Za-z0-9_+-]+){0,2}$`)
	clockEnums = map[string]map[string]bool{
		"style": {"large": true, "seconds": true, "analog": true, "minimal": true},
		"hours": {"auto": true, "12": true, "24": true},
		"date":  {"long": true, "short": true, "numeric": true, "off": true},
	}
)

const maxWallpaper = 8 << 20

func defaultDesktop() desktopPrefs {
	return desktopPrefs{Wallpaper: "auto", Dim: 0, Blur: 0, Order: []string{}, Hidden: []string{},
		Clock: clockPrefs{Style: "large", Hours: "auto", Date: "long", Greeting: true},
		Widgets: []widgetPref{{ID: "clock", On: true}, {ID: "system", On: true}, {ID: "storage", On: true}, {ID: "network", On: true},
			{ID: "disks", On: true}, {ID: "apps", On: false}, {ID: "weather", On: false}}}
}

func (p *desktopPrefs) validate() error {
	if !wallpapers[p.Wallpaper] {
		return userError{"Pick one of the wallpapers."}
	}
	if p.Dim < 0 || p.Dim > 80 || p.Blur < 0 || p.Blur > 40 {
		return userError{"Dim goes from 0 to 80 and blur from 0 to 40."}
	}
	c := p.Clock
	if !clockEnums["style"][c.Style] || !clockEnums["hours"][c.Hours] || !clockEnums["date"][c.Date] {
		return userError{"Pick one of the clock options."}
	}
	if c.TZ2 != "" && (len(c.TZ2) > 64 || !tzName.MatchString(c.TZ2)) {
		return userError{"Pick a time zone from the list."}
	}
	if wt := p.Weather; wt.Lat < -90 || wt.Lat > 90 || wt.Lon < -180 || wt.Lon > 180 || utf8.RuneCountInString(wt.Name) > 80 || controlChar.MatchString(wt.Name) {
		return userError{"Pick a place from the list."}
	}
	if p.Theme != nil {
		if err := p.Theme.validate(); err != nil {
			return err
		}
	}
	seen := map[string]bool{}
	for _, w := range p.Widgets {
		if !widgetIDs[w.ID] || seen[w.ID] {
			return userError{"Unknown widget: " + w.ID}
		}
		if (w.X == nil) != (w.Y == nil) || (w.X != nil && (*w.X < 0 || *w.X > 20000 || *w.Y < 0 || *w.Y > 20000)) {
			return userError{"The widget position is not valid."}
		}
		seen[w.ID] = true
	}
	for _, list := range [][]string{p.Order, p.Hidden} {
		if len(list) > 500 {
			return userError{"Too many tiles."}
		}
		for _, id := range list {
			if !tileID.MatchString(id) {
				return userError{"Unknown tile: " + id}
			}
		}
	}
	return nil
}

func (a *App) loadDesktop(id int64) desktopPrefs {
	p := defaultDesktop()
	if raw := a.store.desktop(id); raw != "" && raw != "{}" {
		saved := defaultDesktop() // fields saved by an older version keep their defaults
		if json.Unmarshal([]byte(raw), &saved) == nil && saved.validate() == nil {
			p = saved
		}
		if p.Onboarded == nil { // used Box before the setup assistant existed: do not ask
			t := true
			p.Onboarded = &t
		}
	}
	// Widgets added in a later version show up at the end.
	have := map[string]bool{}
	for _, w := range p.Widgets {
		have[w.ID] = true
	}
	for _, w := range defaultDesktop().Widgets {
		if !have[w.ID] {
			p.Widgets = append(p.Widgets, w)
		}
	}
	if p.Order == nil {
		p.Order = []string{}
	}
	if p.Hidden == nil {
		p.Hidden = []string{}
	}
	return p
}

func (a *App) desktopGet(w http.ResponseWriter, r *http.Request) {
	id, _ := a.currentUser(r)
	writeJSON(w, http.StatusOK, a.loadDesktop(id))
}

func (a *App) desktopSave(w http.ResponseWriter, r *http.Request) {
	id, _ := a.currentUser(r)
	cur := a.loadDesktop(id)
	in := cur // fields left out of the request keep their saved value
	in.Theme = nil
	if cur.Theme != nil {
		t := *cur.Theme
		in.Theme = &t
	}
	r.Body = http.MaxBytesReader(nil, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if dec.Decode(&in) != nil {
		writeError(w, http.StatusBadRequest, "The request could not be read.")
		return
	}
	if err := in.validate(); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	in.CustomV = cur.CustomV // only an upload changes it
	if in.Wallpaper == "custom" && in.CustomV == 0 {
		writeError(w, http.StatusUnprocessableEntity, "Upload a picture first.")
		return
	}
	a.saveDesktop(w, id, in)
}

func (a *App) saveDesktop(w http.ResponseWriter, id int64, p desktopPrefs) {
	b, _ := json.Marshal(p)
	if err := a.store.setDesktop(id, string(b)); err != nil {
		log.Printf("desktop: %v", err)
		writeError(w, http.StatusInternalServerError, "The desktop could not be saved.")
		return
	}
	writeJSON(w, http.StatusOK, p)
}

func (a *App) wallpaperPath(id int64) string {
	return filepath.Join(a.state, "wallpapers", "user-"+strconv.FormatInt(id, 10))
}

func (a *App) wallpaperUpload(w http.ResponseWriter, r *http.Request) {
	id, _ := a.currentUser(r)
	var in struct{ Data string }
	r.Body = http.MaxBytesReader(nil, r.Body, maxWallpaper*4/3+4096)
	if err := readJSONRaw(r, &in); err != nil {
		writeError(w, http.StatusBadRequest, "The picture can have at most 8 MB.")
		return
	}
	_, b64, _ := strings.Cut(in.Data, ",")
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(b) == 0 || len(b) > maxWallpaper || !wallpaperOK(b) {
		writeError(w, http.StatusUnprocessableEntity, "Use a JPEG, PNG or WebP picture up to 8 MB.")
		return
	}
	p := a.wallpaperPath(id)
	os.MkdirAll(filepath.Dir(p), 0o700)
	if err := os.WriteFile(p, b, 0o600); err != nil {
		log.Printf("wallpaper: %v", err)
		writeError(w, http.StatusInternalServerError, "The picture could not be saved.")
		return
	}
	prefs := a.loadDesktop(id)
	prefs.Wallpaper, prefs.CustomV = "custom", time.Now().Unix()
	a.audit(r, "desktop.wallpaper", "")
	a.saveDesktop(w, id, prefs)
}

// Only raster pictures: SVG could carry script.
func wallpaperOK(b []byte) bool {
	ct := http.DetectContentType(b)
	return ct == "image/png" || ct == "image/jpeg" || ct == "image/webp"
}

func (a *App) wallpaperGet(w http.ResponseWriter, r *http.Request) {
	id, _ := a.currentUser(r)
	b, err := os.ReadFile(a.wallpaperPath(id))
	if err != nil || !wallpaperOK(b) {
		writeError(w, http.StatusNotFound, "No picture uploaded.")
		return
	}
	h := w.Header()
	h.Set("Content-Type", http.DetectContentType(b))
	h.Set("Cache-Control", "private, max-age=31536000, immutable") // the URL carries ?v=
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Write(b)
}
