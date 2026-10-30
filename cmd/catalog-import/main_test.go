package main

import (
	"os"
	"strings"
	"testing"

	"github.com/NexoVirt/nexovirt-box/internal/catalog"
)

func TestDefaultPasswordsBecomeSharedSecrets(t *testing.T) {
	src := `
services:
  app:
    image: wordpress
    ports: ["8080:80"]
    environment:
      WORDPRESS_DB_USER: casaos
      WORDPRESS_DB_PASSWORD: casaos
      DB_URL: mysql://casaos:casaos@db/wp
  db:
    image: mysql
    privileged: false
    environment:
      - MYSQL_USER=casaos
      - MYSQL_PASSWORD=casaos
`
	a := &catalog.App{Summary: catalog.Summary{Name: "WP"}, MainPort: 8080}
	if err := processCompose(a, []byte(src), "casaos", 8080, false); err != nil {
		t.Fatal(err)
	}
	c := a.Compose
	if !strings.Contains(c, "WORDPRESS_DB_PASSWORD: ${SECRET_WORDPRESS_DB_PASSWORD}") ||
		!strings.Contains(c, "MYSQL_PASSWORD=${SECRET_WORDPRESS_DB_PASSWORD}") ||
		!strings.Contains(c, "mysql://casaos:${SECRET_WORDPRESS_DB_PASSWORD}@db/wp") ||
		!strings.Contains(c, "WORDPRESS_DB_USER: casaos") || !strings.Contains(c, "${APP_PORT}:80") {
		t.Fatalf("compose:\n%s", c)
	}
}

func TestLiteralUsedInCommandIsReplacedThereToo(t *testing.T) {
	src := `
services:
  redis:
    image: redis
    command: redis-server --requirepass hunter22
    environment:
      REDIS_PASSWORD: hunter22
`
	a := &catalog.App{Summary: catalog.Summary{Name: "R"}}
	processCompose(a, []byte(src), "casaos", 0, false)
	if !strings.Contains(a.Compose, "--requirepass ${SECRET_REDIS_PASSWORD}") || strings.Contains(a.Compose, "hunter22") {
		t.Fatalf("password not replaced in the command:\n%s", a.Compose)
	}
}

func TestPrivilegedSkipped(t *testing.T) {
	for _, src := range []string{
		"services:\n  a:\n    image: x\n    privileged: true\n",
		"services:\n  a:\n    image: x\n    network_mode: host\n",
	} {
		a := &catalog.App{Summary: catalog.Summary{Name: "P"}}
		if err := processCompose(a, []byte(src), "casaos", 0, false); err != errPrivileged {
			t.Fatalf("not skipped: %q", src)
		}
	}
}

func TestMainImageIgnoresRegistryTagAndHelpers(t *testing.T) {
	c := "services:\n  db:\n    image: postgres:16\n  app:\n    image: lscr.io/linuxserver/jellyfin:10.9\n"
	if got := mainImage(c); got != "linuxserver/jellyfin" {
		t.Fatalf("got %q", got)
	}
	if mainImage("services:\n  a:\n    image: docker.io/library/ghost@sha256:abc\n") != "ghost" {
		t.Fatal("library prefix or digest kept")
	}
	if mainImage("services:\n  a:\n    image: adguard/adguardhome:latest\n") != mainImage("services:\n  b:\n    image: docker.io/adguard/adguardhome\n") {
		t.Fatal("same image did not match")
	}
}

func TestCasaTipBecomesNotes(t *testing.T) {
	dir := t.TempDir() + "/Alist"
	os.MkdirAll(dir, 0o755)
	os.WriteFile(dir+"/docker-compose.yml", []byte(`
services:
  alist:
    image: xhofe/alist:v3.40.0
    ports: ["5244:5244"]
x-casaos:
  main: alist
  port_map: "5244"
  title: {en_us: Alist}
  tips:
    before_install:
      en_us: After installation, please check the initial password in the container log (Settings - TopRight - Terminal and Logs - Logs).
      zh_CN: x
`), 0o644)
	a, err := importCasa(dir, false)
	if err != nil {
		t.Fatal(err)
	}
	if a.Notes != "After installation, please check the initial password in the container log (My Apps, Logs)." {
		t.Fatalf("notes %q", a.Notes)
	}
	a.ID = "alist"
	applyFirstLogin(a)
	if a.FirstLogin == nil || a.FirstLogin.Username != "admin" {
		t.Fatalf("first login %+v", a.FirstLogin)
	}
	if _, err := a.FirstLogin.LogRegexp(); err != nil {
		t.Fatal(err)
	}
}

func TestOddTipsShapeDoesNotDropApp(t *testing.T) {
	dir := t.TempDir() + "/X"
	os.MkdirAll(dir, 0o755)
	os.WriteFile(dir+"/docker-compose.yml", []byte("services:\n  x:\n    image: x/x:1\nx-casaos:\n  main: x\n  title: {en_us: X}\n  tips: just a string\n"), 0o644)
	if a, err := importCasa(dir, false); err != nil || a == nil || a.Notes != "" {
		t.Fatalf("%v %+v", err, a)
	}
}

func TestTipWithReplacedPasswordIsDropped(t *testing.T) {
	a := &catalog.App{Summary: catalog.Summary{Name: "DB"}, Notes: "Default user: `casaos` Default password: `casaos`"}
	processCompose(a, []byte("services:\n  db:\n    image: mariadb\n    environment:\n      MYSQL_PASSWORD: casaos\n"), "casaos", 0, false)
	if a.Notes != "" {
		t.Fatalf("notes kept: %q", a.Notes)
	}
}

func TestKnownFirstLoginIsValid(t *testing.T) {
	for id, fl := range knownFirstLogin {
		fl := fl
		if _, err := fl.LogRegexp(); err != nil {
			t.Errorf("%s: %v", id, err)
		}
		if _, err := fl.FileRegexp(); err != nil {
			t.Errorf("%s: %v", id, err)
		}
		if _, err := fl.FilePath(); err != nil {
			t.Errorf("%s: %v", id, err)
		}
	}
}
