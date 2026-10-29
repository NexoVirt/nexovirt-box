package box

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/pem"
	"os"
	"strings"
	"testing"

	"github.com/NexoVirt/nexovirt-box/internal/catalog"
)

// deploy/get.sh verifies releases with openssl and its own copy of the release key.
func TestGetShUsesTheReleaseKey(t *testing.T) {
	b, err := os.ReadFile("../../deploy/get.sh")
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i := strings.Index(s, "-----BEGIN PUBLIC KEY-----")
	if i < 0 {
		t.Fatal("no public key in get.sh")
	}
	end := "-----END PUBLIC KEY-----"
	j := strings.Index(s[i:], end)
	if j < 0 {
		t.Fatal("no end of the public key in get.sh")
	}
	blk, _ := pem.Decode([]byte(s[i:i+j+len(end)] + "\n"))
	if blk == nil {
		t.Fatal("bad PEM in get.sh")
	}
	k, err := x509.ParsePKIXPublicKey(blk.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if pk, ok := k.(ed25519.PublicKey); !ok || !pk.Equal(catalog.ReleasePublicKey) {
		t.Fatal("get.sh does not carry the release key from internal/catalog/key.go")
	}
}
