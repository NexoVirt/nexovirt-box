// NexoVirt Box: a web UI on top of a normal Linux server.
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

//go:embed all:web/dist
var webDist embed.FS

var version = "dev"

const defaultSocket = "/run/nexovirt-box/helper.sock"

func main() {
	syscall.Umask(0o077) // everything Box writes is private unless it says otherwise
	if len(os.Args) > 1 && os.Args[1] == "helper" {
		runHelper(os.Args[2:])
		return
	}
	listen := flag.String("listen", ":8443", "address to listen on")
	state := flag.String("state", "/var/lib/nexovirt-box/web", "state directory (database, certificate, setup token, catalog)")
	socket := flag.String("helper", defaultSocket, "unix socket of the root helper")
	trusted := flag.String("trusted-proxies", "", "comma separated IPs/CIDRs of reverse proxies allowed to set X-Forwarded-For")
	catalogURL := flag.String("catalog-url", "", "base URL of a signed app catalog to sync from (index.json + apps/)")
	dev := flag.Bool("dev", false, "plain HTTP for local development (never in production)")
	flag.Parse()

	if err := os.MkdirAll(*state, 0o700); err != nil {
		log.Fatalf("state directory: %v", err)
	}
	store, err := openStore(filepath.Join(*state, "box.db"))
	if err != nil {
		log.Fatalf("database: %v", err)
	}
	defer store.db.Close()

	app := &App{
		store:   store,
		limiter: newLimiter(store, 5, time.Minute),
		secure:  !*dev,
		sampler: newSampler(2 * time.Second),
		trusted: parseCIDRs(*trusted),
		helper:  &helperClient{socket: *socket},
		catalog: newCatalogStore(*state, *catalogURL),
		jobs:    newJobs(),
	}
	if u := store.setting("catalog_url"); u != "" {
		app.catalog.setURL(u) // set in the UI; wins over -catalog-url
	}
		if err := app.ensureSetupToken(*state); err != nil {
		log.Fatalf("setup token: %v", err)
	}
	go app.sampler.run()

	ui, err := fs.Sub(webDist, "web/dist")
	if err != nil {
		log.Fatal(err)
	}
	srv := &http.Server{
		Addr:              *listen,
		Handler:           app.routes(ui),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Printf("NexoVirt Box %s listening on %s", version, *listen)
	if *dev {
		log.Fatal(srv.ListenAndServe())
	}
	cert, key, err := ensureCert(*state)
	if err != nil {
		log.Fatalf("certificate: %v", err)
	}
	log.Fatal(srv.ListenAndServeTLS(cert, key))
}

// runHelper is the root half: `nexovirt-box helper`.
func runHelper(args []string) {
	fl := flag.NewFlagSet("helper", flag.ExitOnError)
	socket := fl.String("socket", defaultSocket, "unix socket to listen on")
	webUser := fl.String("user", "nexovirt-box", "the unprivileged web user allowed to connect")
	apps := fl.String("apps", "/var/lib/nexovirt-box/apps", "root-owned directory for installed apps")
	webState := fl.String("web-state", "/var/lib/nexovirt-box/web", "web state directory (synced catalog)")
	fl.Parse(args)

	u, err := user.Lookup(*webUser)
	if err != nil {
		log.Fatalf("helper: web user %q: %v", *webUser, err)
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	if err := os.MkdirAll(*apps, 0o700); err != nil {
		log.Fatalf("helper: apps directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(*socket), 0o755); err != nil {
		log.Fatalf("helper: socket directory: %v", err)
	}
	h := &helperServer{allowUID: uid, appsDir: *apps, webState: *webState, proofs: newProofKeeper()}
	ln, err := h.listen(*socket, gid)
	if err != nil {
		log.Fatalf("helper: %v", err)
	}
	log.Printf("NexoVirt Box helper %s listening on %s (allowed user %s)", version, *socket, *webUser)
	h.serve(ln)
}
