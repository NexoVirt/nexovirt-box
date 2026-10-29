//go:build !devkey

package catalog

import (
	"crypto/ed25519"
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

// The dev key in keys/ is public on purpose: a build without -tags devkey must never trust it,
// neither for the catalog nor for Box self-updates.
func TestReleaseBuildDoesNotTrustDevKey(t *testing.T) {
	b, err := os.ReadFile("../../keys/catalog-dev.key")
	if err != nil {
		t.Fatal(err)
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(b)))
	if err != nil || len(seed) != ed25519.SeedSize {
		t.Fatal("bad dev key")
	}
	dev := ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey)
	for _, k := range PublicKeys {
		if k.Equal(dev) {
			t.Fatal("the dev key is trusted for the catalog without -tags devkey")
		}
	}
	if ReleasePublicKey.Equal(dev) {
		t.Fatal("the dev key is the release key")
	}
}
