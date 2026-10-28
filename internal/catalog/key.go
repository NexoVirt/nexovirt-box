package catalog

import (
	"crypto/ed25519"
	"encoding/base64"
)

// PublicKey verifies the app catalog. It belongs to the development key in keys/catalog-dev.key;
// replace it with the release key before a public release.
var PublicKey = mustKey("6o6iHESeqHocBauOqLTODIPzX32sLrotLYwjEl78jmw=")

func mustKey(b64 string) ed25519.PublicKey {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(b) != ed25519.PublicKeySize {
		panic("catalog: bad public key")
	}
	return ed25519.PublicKey(b)
}
