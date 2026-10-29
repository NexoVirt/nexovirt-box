// release-sign writes the signed self-update manifest for a Box release:
//
//	go run ./cmd/release-sign -version 0.2.0 -base https://example.com/box -out dist \
//	    bin/nexovirt-box-linux-amd64 bin/nexovirt-box-linux-arm64 deploy/install.sh
//
// It copies the binaries into -out, writes latest.json with their sha256 and URLs under -base,
// and latest.json.sig signed with the release key (kept outside the repository).
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NexoVirt/nexovirt-box/internal/catalog"
)

func main() {
	ver := flag.String("version", "", "release version")
	base := flag.String("base", "", "public base URL the files are served from")
	out := flag.String("out", "dist", "output directory")
	notes := flag.String("notes", "", "release notes")
	keyPath := flag.String("key", os.ExpandEnv("$HOME/.config/nexovirt/box-release.key"), "base64 Ed25519 seed")
	flag.Parse()
	if *ver == "" || *base == "" || flag.NArg() == 0 {
		log.Fatal("usage: release-sign -version V -base URL [-out dir] binaries...")
	}
	seedB64, err := os.ReadFile(*keyPath)
	if err != nil {
		log.Fatal(err)
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(seedB64)))
	if err != nil || len(seed) != ed25519.SeedSize {
		log.Fatal("bad key")
	}
	priv := ed25519.NewKeyFromSeed(seed)
	if !bytes.Equal(priv.Public().(ed25519.PublicKey), catalog.ReleasePublicKey) {
		log.Fatal("the key does not match the release key in internal/catalog/key.go")
	}
	os.MkdirAll(*out, 0o755)
	files := map[string]map[string]string{}
	for _, p := range flag.Args() {
		b, err := os.ReadFile(p)
		if err != nil {
			log.Fatal(err)
		}
		// Binaries (nexovirt-box-linux-<arch>) are keyed by arch and get the version in their
		// name; any other file (install.sh) is keyed by its name, so get.sh can verify it too.
		key, name := filepath.Base(p), filepath.Base(p)
		if strings.HasPrefix(name, "nexovirt-box-linux-") {
			key, name = name[strings.LastIndex(name, "-")+1:], name+"-"+*ver
		}
		if err := os.WriteFile(filepath.Join(*out, name), b, 0o644); err != nil {
			log.Fatal(err)
		}
		sum := sha256.Sum256(b)
		files[key] = map[string]string{"url": strings.TrimRight(*base, "/") + "/" + name, "sha256": hex.EncodeToString(sum[:])}
	}
	m, _ := json.MarshalIndent(map[string]any{"version": *ver, "notes": *notes, "published_at": time.Now().Unix(), "files": files}, "", "  ")
	os.WriteFile(filepath.Join(*out, "latest.json"), m, 0o644)
	os.WriteFile(filepath.Join(*out, "latest.json.sig"), []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(priv, m))+"\n"), 0o644)
	fmt.Printf("wrote %s/latest.json (+ .sig) for %s\n", *out, *ver)
}
