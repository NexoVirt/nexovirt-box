package main

import (
	"strings"
	"testing"

	"github.com/nexovirt/box/internal/catalog"
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

func TestLiteralUsedElsewhereStays(t *testing.T) {
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
	if strings.Contains(a.Compose, "SECRET_") {
		t.Fatalf("replaced a password that is also in the command:\n%s", a.Compose)
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
