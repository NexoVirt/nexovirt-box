package catalog

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func fix(t *testing.T, compose string) (*App, []string) {
	t.Helper()
	a := &App{Summary: Summary{ID: "t"}, Compose: compose}
	lits, err := FixSecrets(a)
	if err != nil {
		t.Fatal(err)
	}
	return a, lits
}

func mustContain(t *testing.T, c string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(c, w) {
			t.Fatalf("missing %q in:\n%s", w, c)
		}
	}
}

// The claude-code-container case: a UUID password, used in a guard script and shared by a
// Caddy sidecar config, with the store's own "still the catalog value" refusal.
func TestStrongLiteralReplacedEverywhereAndGuardDropped(t *testing.T) {
	a, lits := fix(t, `services:
  claude:
    image: x
    environment:
      ANTHROPIC_API_KEY: ""
      AUTH_PASSWORD: "9c93e0aa-ebe4-4737-b222-d6445c64da29"
    entrypoint:
      - /bin/sh
      - -c
      - |
        if [ -z "$$AUTH_PASSWORD" ] || [ "$$AUTH_PASSWORD" = "9c93e0aa-ebe4-4737-b222-d6445c64da29" ]; then
          exit 1
        fi
        exec /init
  caddy:
    image: caddy
    configs: [caddyfile]
configs:
  caddyfile:
    content: |
      basic_auth { admin 9c93e0aa-ebe4-4737-b222-d6445c64da29 }
`)
	if len(lits) != 1 || len(a.Questions) != 1 {
		t.Fatalf("lits %v questions %+v", lits, a.Questions)
	}
	q := a.Questions[0]
	if q.Key != "SECRET_AUTH_PASSWORD" || q.Type != "password" || !q.Generate {
		t.Fatalf("question %+v", q)
	}
	mustContain(t, a.Compose, "AUTH_PASSWORD: ${SECRET_AUTH_PASSWORD}", `if [ -z "$$AUTH_PASSWORD" ]; then`,
		`basic_auth { admin ${SECRET_AUTH_PASSWORD} }`, `ANTHROPIC_API_KEY: ""`)
	if strings.Contains(a.Compose, "9c93e0aa") {
		t.Fatalf("literal left:\n%s", a.Compose)
	}
}

// A weak literal is also the user, database and host name: only the secret spots change, and
// list syntax, URLs and redis flags in every service use the same variable.
func TestWeakLiteralOnlySecretSpots(t *testing.T) {
	a, _ := fix(t, `services:
  app:
    image: gitea/gitea
    container_name: gitea
    environment:
      - GITEA__database__USER=gitea
      - GITEA__database__PASSWD=gitea
      - 'DB_URL=postgres://gitea:gitea@gitea-db/gitea'
    command: ["redis-server", "--requirepass", "gitea"]
  gitea-db:
    image: postgres
    environment:
      POSTGRES_USER: gitea
      POSTGRES_PASSWORD: 'gitea'
      POSTGRES_DB: gitea
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U gitea -d gitea && redis-cli -a gitea ping"]
  conf:
    image: x
    environment:
      CONFIG: |
        db:
          user: gitea
          password: gitea
`)
	v := "${SECRET_GITEA_DATABASE_PASSWD}"
	mustContain(t, a.Compose, "GITEA__database__PASSWD="+v, "POSTGRES_PASSWORD: "+v,
		"postgres://gitea:"+v+"@gitea-db/gitea", `"--requirepass", "`+v+`"`, "redis-cli -a "+v+" ping",
		"password: "+v, "user: gitea", "GITEA__database__USER=gitea", "POSTGRES_DB: gitea", "pg_isready -U gitea -d gitea",
		"image: gitea/gitea", "container_name: gitea")
	if len(a.Questions) != 1 {
		t.Fatalf("questions %+v", a.Questions)
	}
	if left, _ := LiteralSecrets(a.Compose); len(left) != 0 {
		t.Fatalf("left %v", left)
	}
}

func TestSettingsAboutSecretsAreNotSecrets(t *testing.T) {
	for _, kv := range [][2]string{{"AUTH_MODE", "local"}, {"AUTH_ENABLED", "true"}, {"NEXTAUTH_URL", "http://x:1"},
		{"POSTGRES_HOST_AUTH_METHOD", "trust"}, {"JWT_EXP", "1"}, {"APIKEY_PATH", "/root/.x"}, {"AUTH_USERNAME", "admin"},
		{"S3_ACCESS_KEY", "indelible"}, {"MARIADB_RANDOM_ROOT_PASSWORD", "true"}, {"ANTHROPIC_API_KEY", ""},
		{"SECRET_KEY", "${X}"}, {"BASIC_AUTH", "true"}, {"ALLOW_PASSWORD_RESET", "false"}, {"REFRESH_TOKEN_EXPIRY", "(1000 * 60) * 7"},
		{"ACCESS_TOKEN_LIFETIME_MIN", "5"}, {"OIDC_TOKEN_URI", "http://x/t"}, {"API_ACCESS_TOKEN_TTL", "604800"},
		{"POSTGRES_HOST_AUTH_METHODL", "trust"}, {"PASSWORD_FILE", "run/secrets/x"}, {"MINIO_ACCESS_KEY_ID", "minioadmin"}} {
		if IsLiteralSecret(kv[0], kv[1]) {
			t.Errorf("%s=%s flagged", kv[0], kv[1])
		}
	}
	for _, kv := range [][2]string{{"POSTGRES_PASSWORD", "tipi"}, {"REDISCLI_AUTH", "x1"}, {"PRIVATE_KEY", "LS0t"},
		{"JWT_SECRET", "changeme"}, {"API_KEY", "dify-sandbox"}, {"PASSWORD", `"casaos"`}, {"SALT", "abc"},
		{"SUPERUSER_PASSWORD", "x"}, {"SEAFILE_ADMIN_PASSWORD", "asecret"}, {"SECURITY_INITIALLOGIN_PASSWORD", "x"},
		{"USER_PASSWORD", "root"}, {"ASSET_COOKIE_SECRET", "32e4"}, {"NEO4J_AUTH", "neo4j/password"},
		{"MINIO_SECRET_ACCESS_KEY", "minioadmin"}, {"S3_ACCESS_KEY", "change-me-s3-access-J4hN7qX2"}} {
		if !IsLiteralSecret(kv[0], kv[1]) {
			t.Errorf("%s=%s not flagged", kv[0], kv[1])
		}
	}
}

func TestFixSecretsIdempotentAndUniqueNames(t *testing.T) {
	a := &App{Summary: Summary{ID: "t"}, Questions: []Question{{Key: "SECRET_DB_PASSWORD"}},
		Compose: "services:\n  a:\n    image: x\n    environment:\n      DB_PASSWORD: hunter22\n"}
	if _, err := FixSecrets(a); err != nil {
		t.Fatal(err)
	}
	mustContain(t, a.Compose, "DB_PASSWORD: ${SECRET_DB_PASSWORD_2}")
	before := a.Compose
	if lits, _ := FixSecrets(a); lits != nil || a.Compose != before || len(a.Questions) != 2 {
		t.Fatalf("second run changed something: %v\n%s", lits, a.Compose)
	}
}

// An S3 access key that is a plain word is the MinIO user name: kept. NEO4J_AUTH keeps its user
// part. A literal that looks like a variable name is not replaced inside the new reference.
func TestAccessKeyUserPassAndNameLikeLiterals(t *testing.T) {
	a, _ := fix(t, `services:
  app:
    image: x
    environment:
      - S3_ACCESS_KEY=minio
      - S3_SECRET_KEY=minio
      - NEO4J_AUTH=neo4j/password
      - NEO4J_PASSWORD=password
      - TELEGRAM_API_KEY=TELEGRAM_API_KEY
  minio:
    image: minio
    environment:
      MINIO_ROOT_USER: minio
      MINIO_ROOT_PASSWORD: minio
`)
	mustContain(t, a.Compose, "S3_ACCESS_KEY=minio", "MINIO_ROOT_USER: minio", "S3_SECRET_KEY=${SECRET_S3_SECRET_KEY}",
		"MINIO_ROOT_PASSWORD: ${SECRET_S3_SECRET_KEY}", "NEO4J_AUTH=neo4j/${SECRET_NEO4J_AUTH}", "NEO4J_PASSWORD=${SECRET_NEO4J_AUTH}",
		"TELEGRAM_API_KEY=${SECRET_TELEGRAM_API_KEY}\n")
	if strings.Count(a.Compose, "SECRET_TELEGRAM_API_KEY") != 1 {
		t.Fatalf("nested reference:\n%s", a.Compose)
	}
}

// The shipped catalog must not carry a public literal secret.
func TestCatalogHasNoLiteralSecrets(t *testing.T) {
	files, _ := filepath.Glob("../../catalog/apps/*.json")
	if len(files) == 0 {
		t.Skip("no catalog")
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		var a App
		if err := json.Unmarshal(b, &a); err != nil {
			t.Fatal(err)
		}
		if left, err := LiteralSecrets(a.Compose); err != nil || len(left) > 0 {
			t.Errorf("%s: literal secrets %v %v", a.ID, left, err)
		}
	}
}

func TestBase64KeyKeepsItsShape(t *testing.T) {
	a, _ := fix(t, "services:\n  a:\n    image: x\n    environment:\n      ENTE_KEY_ENCRYPTION: yvmG/RnzKrbCb9L3mgsmoxXr9H7i2Z4qlbT0mL3ln4w=\n")
	if q := a.Questions[0]; q.Format != "base64" || q.Min != 32 || !q.Generate {
		t.Fatalf("question %+v", q)
	}
}
