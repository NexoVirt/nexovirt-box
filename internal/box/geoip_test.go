package box

import (
	"bytes"
	"compress/gzip"
	"testing"
)

func TestDeviceOf(t *testing.T) {
	for ua, want := range map[string]string{
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0 Safari/537.36 Edg/130.0": "Edge on Windows",
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 14_5) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.5 Safari/605.1.15":    "Safari on macOS",
		"Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0":                                                "Firefox on Linux",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 Version/18.0 Mobile/15E148 Safari/604.1":   "Safari on iOS",
		"Mozilla/5.0 (Linux; Android 15) AppleWebKit/537.36 Chrome/130.0 Mobile Safari/537.36":                                  "Chrome on Android",
		"": "Unknown browser",
	} {
		if got := deviceOf(ua); got != want {
			t.Errorf("deviceOf(%.40q) = %q, want %q", ua, got, want)
		}
	}
}

func TestGeoOffAndBadDownload(t *testing.T) {
	app, _, _ := newTestApp(t)
	app.state = t.TempDir()
	if p := app.placeOf("8.8.8.8"); p != "" {
		t.Fatalf("place without the feature: %q", p)
	}
	app.store.setSetting(geoKey, "1")
	if p := app.placeOf("192.168.1.10"); p != "" {
		t.Fatalf("place for a private address: %q", p)
	}
	if d := app.loginDetails("8.8.8.8", "curl/8.0"); d != "curl" {
		t.Fatalf("details without a database: %q", d)
	}
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	zw.Write([]byte("not a database"))
	zw.Close()
	if err := app.saveGeo(&b); err == nil {
		t.Fatal("a broken database was accepted")
	}
}
