// NexoVirt Box: a web UI on top of a normal Linux server.
package main

import (
	"embed"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

//go:embed all:web/dist
var webDist embed.FS

var version = "dev"

func main() {
	listen := flag.String("listen", ":8443", "address to listen on")
	state := flag.String("state", "/var/lib/nexovirt-box", "state directory (database, certificate, setup token)")
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
		limiter: newLimiter(5, time.Minute),
		secure:  !*dev,
		sampler: newSampler(2 * time.Second),
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
