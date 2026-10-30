package box

import (
	"strings"
	"testing"
	"time"
)

func TestParseWeather(t *testing.T) {
	w, err := parseWeather([]byte(`{"current":{"temperature_2m":12.4,"weather_code":61,"wind_speed_10m":9.1,"is_day":1},"daily":{"temperature_2m_max":[15.2],"temperature_2m_min":[7.9]}}`))
	if err != nil || w.Temp != 12.4 || w.Code != 61 || !w.IsDay || w.Max != 15.2 || w.Min != 7.9 {
		t.Fatalf("%+v %v", w, err)
	}
	if _, err := parseWeather([]byte(`{"error":true}`)); err == nil {
		t.Fatal("broken answer accepted")
	}
}

func TestWidgetPrefsValidation(t *testing.T) {
	x, y, neg := 120, 300, -5
	p := defaultDesktop()
	p.Widgets[0].X, p.Widgets[0].Y = &x, &y
	p.Weather = weatherPrefs{Name: "Berlin, Germany", Lat: 52.52, Lon: 13.41}
	if err := p.validate(); err != nil {
		t.Fatal(err)
	}
	bad := defaultDesktop()
	bad.Widgets[0].X = &x // y missing
	if bad.validate() == nil {
		t.Fatal("x without y accepted")
	}
	bad.Widgets[0].Y = &neg
	if bad.validate() == nil {
		t.Fatal("negative position accepted")
	}
	bad = defaultDesktop()
	bad.Weather = weatherPrefs{Name: "x\ny", Lat: 1}
	if bad.validate() == nil {
		t.Fatal("control character in the place accepted")
	}
	bad.Weather = weatherPrefs{Name: strings.Repeat("a", 10), Lat: 91}
	if bad.validate() == nil {
		t.Fatal("latitude 91 accepted")
	}
}

// The cache holds only weather; each user gets their own place name back.
func TestWeatherCacheHasNoPlaceName(t *testing.T) {
	weatherCache.Store("1.00,2.00", weatherEntry{time.Now(), weatherNow{Temp: 3}})
	now, ok := cachedWeather("1.00,2.00")
	if r := weatherReply("Bob's village", now); !ok || r["place"] != "Bob's village" || now.Temp != 3 {
		t.Fatalf("%v %v", ok, r)
	}
}
