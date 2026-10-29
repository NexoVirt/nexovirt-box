package catalog

import (
	"strings"
	"testing"
)

func TestFixShapesTurnsListMapsIntoStrings(t *testing.T) {
	a := &App{Compose: `services:
  web:
    image: nginx
    environment:
      - HOMEPAGE_ALLOWED_HOSTS: ${APP_DOMAIN},${APP_HOST}:${APP_PORT}
      - PLAIN=1
      - EMPTY:
      - "QUOTED: a: b"
`}
	n, err := FixShapes(a)
	if err != nil || n != 2 {
		t.Fatalf("fixed %d, %v", n, err)
	}
	if errs := ValidateCompose(a.Compose); len(errs) > 0 {
		t.Fatalf("still invalid: %v\n%s", errs, a.Compose)
	}
	for _, want := range []string{"HOMEPAGE_ALLOWED_HOSTS=${APP_DOMAIN},${APP_HOST}:${APP_PORT}", "- PLAIN=1", "- EMPTY\n", "QUOTED: a: b"} {
		if !strings.Contains(a.Compose, want) {
			t.Errorf("missing %q in\n%s", want, a.Compose)
		}
	}
	// Nothing to fix: compose untouched.
	before := a.Compose
	if n, _ := FixShapes(a); n != 0 || a.Compose != before {
		t.Fatalf("second pass changed it")
	}
}

func TestValidateCompose(t *testing.T) {
	ok := `services:
  a:
    image: x
    environment: {A: "1", B: 2, C: null}
    ports: ["80:80", {target: 81, published: "81"}]
    volumes: ["./d:/d", {type: bind, source: ./e, target: /e}]
    labels: [a=b]
  b:
    image: y
    environment: [A=1]
    labels: {a: b}
  c:
    image: z
    environment: &env {A: "1"}
  d:
    image: z
    environment: *env
`
	if errs := ValidateCompose(ok); len(errs) > 0 {
		t.Fatalf("valid compose refused: %v", errs)
	}
	bad := map[string]string{
		"env list of maps": "services:\n  a:\n    image: x\n    environment:\n      - A: 1\n",
		"env map nested":   "services:\n  a:\n    image: x\n    environment:\n      A: {b: c}\n",
		"ports scalar":     "services:\n  a:\n    image: x\n    ports: 80:80\n",
		"ports nested list": "services:\n  a:\n    image: x\n    ports: [[80]]\n",
		"volumes map":      "services:\n  a:\n    image: x\n    volumes: {a: b}\n",
		"no services":      "x: 1\n",
		"service scalar":   "services:\n  a: x\n",
		"labels nested":    "services:\n  a:\n    image: x\n    labels: [{a: b}]\n",
	}
	for name, c := range bad {
		if errs := ValidateCompose(c); len(errs) == 0 {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestFixShapesStoreTypos(t *testing.T) {
	a := &App{Compose: `services:
  web:
    image: nginx
    network-mode: host
    env_file:
      - ${APP_DATA_DIR}/data/app.env
      - .env
    depends_on:
      - web-db
      - cache
  cache:
    image: redis
    depends_on:
      gone:
        condition: service_started
`}
	if errs := ValidateCompose(a.Compose); len(errs) != 4 {
		t.Fatalf("want 4 errors (key, env_file, 2 depends_on), got %v", errs)
	}
	n, err := FixShapes(a)
	if err != nil || n != 4 {
		t.Fatalf("fixed %d, %v\n%s", n, err, a.Compose)
	}
	if errs := ValidateCompose(a.Compose); len(errs) > 0 {
		t.Fatalf("still invalid: %v\n%s", errs, a.Compose)
	}
	for _, want := range []string{"network_mode: host", "required: false", "- .env", "- cache"} {
		if !strings.Contains(a.Compose, want) {
			t.Errorf("missing %q in\n%s", want, a.Compose)
		}
	}
	if strings.Contains(a.Compose, "web-db") || strings.Contains(a.Compose, "gone") {
		t.Errorf("undefined dependency kept:\n%s", a.Compose)
	}
}

func TestNeedsHost(t *testing.T) {
	if !NeedsHost("services:\n  a:\n    image: x\n    network_mode: host\n") || NeedsHost("services:\n  a:\n    image: x\n") {
		t.Fatal("NeedsHost wrong")
	}
}
