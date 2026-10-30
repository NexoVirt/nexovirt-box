package catalog

import (
	"reflect"
	"testing"
)

func TestNestedPairs(t *testing.T) {
	got := NestedPairs([]string{"/d/data", "/d/data/db", "/d/data2", "/s/media", "/s/media/", "/s/media/x/y", "/d/data"})
	want := [][2]string{{"/d/data/db", "/d/data"}, {"/s/media/x/y", "/s/media"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestBindSources(t *testing.T) {
	compose := `services:
  a:
    volumes:
      - ${APP_DATA_DIR}:/x
      - '${APP_DATA_DIR}/db:/var/lib/db:rw'
      - ${BITCOIND_DIR:-${APP_DATA_DIR}/../btc}:/btc:ro
      - named:/n
      - ./rel:/r
      - type: bind
        source: ${ROOT_FOLDER_HOST}/media
        target: /media
volumes:
  named: {}
  dev:
    driver_opts: {type: none, o: bind, device: /srv/x}
`
	got, err := BindSources(compose, map[string]string{"APP_DATA_DIR": "/b/apps/a/data", "ROOT_FOLDER_HOST": "/b/shared"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/b/apps/a/data", "/b/apps/a/data/db", "/b/apps/a/btc", "/b/shared/media", "/srv/x"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v", got)
	}
}

func TestValidLayout(t *testing.T) {
	ok := []LayoutStep{{From: "etc/x", To: "x"}, {To: "halo", Except: []string{"db"}}}
	if err := ValidLayout(ok); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]LayoutStep{{{To: "../x"}}, {{From: "/etc", To: "x"}}, {{To: "a/b"}}, {{To: "x", Except: []string{"a/b"}}}, {{From: "a", To: "a"}}, {{To: ""}}} {
		if ValidLayout(bad) == nil {
			t.Errorf("%v accepted", bad)
		}
	}
}
