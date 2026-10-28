// catalog-resign rebuilds index.json from the app files listed in it and signs it again, without
// touching the apps. Use it after a merge or a hand edit of catalog/apps/*.json.
package main

import (
	"encoding/json"
	"flag"
	"log"
	"os"
	"path/filepath"

	"github.com/nexovirt/box/internal/catalog"
)

func main() {
	out := flag.String("out", "catalog", "catalog directory")
	keyPath := flag.String("key", os.ExpandEnv("$HOME/.config/nexovirt/box-catalog.key"), "base64 Ed25519 seed (release key, kept outside the repository)")
	flag.Parse()

	priv, err := catalog.LoadSigningKey(*keyPath)
	if err != nil {
		log.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(*out, "index.json"))
	if err != nil {
		log.Fatal(err)
	}
	var idx catalog.Index
	if err := json.Unmarshal(raw, &idx); err != nil {
		log.Fatal(err)
	}
	apps := make([]*catalog.App, 0, len(idx.Apps))
	for _, e := range idx.Apps {
		b, err := os.ReadFile(filepath.Join(*out, e.File))
		if err != nil {
			log.Fatal(err)
		}
		var a catalog.App
		if err := json.Unmarshal(b, &a); err != nil {
			log.Fatalf("%s: %v", e.File, err)
		}
		apps = append(apps, &a)
	}
	if err := catalog.WriteDir(*out, priv, apps); err != nil {
		log.Fatal(err)
	}
	log.Printf("re-signed %d apps", len(apps))
}
