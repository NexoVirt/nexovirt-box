package box

// Weather for the desktop widget through Open-Meteo (no API key; open-meteo.com, data CC BY
// 4.0). The browser never talks to it: Box fetches on the server, only for the place the user
// picked, and caches each answer for 20 minutes.

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var (
	weatherBase   = "https://api.open-meteo.com/v1/forecast"
	geocodingBase = "https://geocoding-api.open-meteo.com/v1/search"
	weatherCache  sync.Map // "lat,lon" -> weatherEntry
)

type weatherEntry struct {
	at  time.Time
	now weatherNow // only the weather: the place name is the asking user's and added per request
}

type weatherNow struct {
	Temp    float64 `json:"temp"`
	Code    int     `json:"code"`
	Wind    float64 `json:"wind"`
	Max     float64 `json:"max"`
	Min     float64 `json:"min"`
	IsDay   bool    `json:"is_day"`
	Updated int64   `json:"updated"`
}

// parseWeather turns Open-Meteo's forecast answer into the widget's fields.
func parseWeather(b []byte) (weatherNow, error) {
	var r struct {
		Current struct {
			Temperature float64 `json:"temperature_2m"`
			WeatherCode int     `json:"weather_code"`
			Wind        float64 `json:"wind_speed_10m"`
			IsDay       int     `json:"is_day"`
		} `json:"current"`
		Daily struct {
			Max []float64 `json:"temperature_2m_max"`
			Min []float64 `json:"temperature_2m_min"`
		} `json:"daily"`
	}
	if err := json.Unmarshal(b, &r); err != nil || len(r.Daily.Max) == 0 || len(r.Daily.Min) == 0 {
		return weatherNow{}, fmt.Errorf("unexpected weather answer")
	}
	return weatherNow{Temp: r.Current.Temperature, Code: r.Current.WeatherCode, Wind: r.Current.Wind, IsDay: r.Current.IsDay == 1,
		Max: r.Daily.Max[0], Min: r.Daily.Min[0], Updated: time.Now().Unix()}, nil
}

func (a *App) weather(w http.ResponseWriter, r *http.Request) {
	id, _ := a.currentUser(r)
	wp := a.loadDesktop(id).Weather
	if wp.Name == "" {
		writeError(w, http.StatusNotFound, "Pick a place for the weather first.")
		return
	}
	key := strconv.FormatFloat(wp.Lat, 'f', 2, 64) + "," + strconv.FormatFloat(wp.Lon, 'f', 2, 64)
	now, ok := cachedWeather(key)
	if !ok {
		lat, lon, _ := strings.Cut(key, ",")
		q := url.Values{"latitude": {lat}, "longitude": {lon}, "current": {"temperature_2m,weather_code,wind_speed_10m,is_day"},
			"daily": {"temperature_2m_max,temperature_2m_min"}, "timezone": {"auto"}, "forecast_days": {"1"}}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		b, err := fetchLimited(ctx, weatherBase+"?"+q.Encode(), 256<<10)
		if err != nil {
			writeError(w, http.StatusBadGateway, "The weather service did not answer. Try again later.")
			return
		}
		if now, err = parseWeather(b); err != nil {
			writeError(w, http.StatusBadGateway, "The weather service sent something Box cannot read.")
			return
		}
		weatherCache.Store(key, weatherEntry{time.Now(), now})
	}
	writeJSON(w, http.StatusOK, weatherReply(wp.Name, now))
}

func cachedWeather(key string) (weatherNow, bool) {
	if e, ok := weatherCache.Load(key); ok && time.Since(e.(weatherEntry).at) < 20*time.Minute {
		return e.(weatherEntry).now, true
	}
	return weatherNow{}, false
}

func weatherReply(placeName string, now weatherNow) map[string]any {
	return map[string]any{"place": placeName, "now": now}
}

type place struct {
	Name    string  `json:"name"`
	Country string  `json:"country"`
	Admin   string  `json:"admin1"`
	Lat     float64 `json:"latitude"`
	Lon     float64 `json:"longitude"`
}

// weatherPlaces looks up places by name for the widget settings.
func (a *App) weatherPlaces(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("q"))
	if utf8.RuneCountInString(name) < 2 || len(name) > 80 || controlChar.MatchString(name) {
		writeJSON(w, http.StatusOK, []place{})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	b, err := fetchLimited(ctx, geocodingBase+"?"+url.Values{"name": {name}, "count": {"6"}, "format": {"json"}}.Encode(), 256<<10)
	if err != nil {
		writeError(w, http.StatusBadGateway, "The place search did not answer. Try again later.")
		return
	}
	var res struct{ Results []place }
	json.Unmarshal(b, &res)
	if res.Results == nil {
		res.Results = []place{}
	}
	writeJSON(w, http.StatusOK, res.Results)
}
