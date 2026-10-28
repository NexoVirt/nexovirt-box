package catalog

import (
	"crypto/ed25519"
	"encoding/base64"
)

// PublicKeys verify the app catalog. The release key's private half lives offline (never in this
// repository); dev builds (-tags devkey) also accept keys/catalog-dev.key, see key_dev.go.
var PublicKeys = []ed25519.PublicKey{mustKey("zZo3pRySe49pMmzBcSAJF6rGDgj53jX3jTrRljy37Tg=")}

// ReleasePublicKey verifies Box self-updates (release manifest). Its private half is offline too.
var ReleasePublicKey = mustKey("ovGXLAbEZ+DXv7ZpNQcrdKguIbLJ1nn8eaTI7CBf+L8=")

func mustKey(b64 string) ed25519.PublicKey {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil || len(b) != ed25519.PublicKeySize {
		panic("catalog: bad public key")
	}
	return ed25519.PublicKey(b)
}
