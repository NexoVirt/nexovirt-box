package catalog

import (
	"bytes"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LoadSigningKey reads a base64 Ed25519 seed and refuses keys Box would not accept.
func LoadSigningKey(path string) (ed25519.PrivateKey, error) {
	seedB64, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(seedB64)))
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("bad key in %s", path)
	}
	priv := ed25519.NewKeyFromSeed(seed)
	for _, p := range PublicKeys {
		if bytes.Equal(priv.Public().(ed25519.PublicKey), p) {
			return priv, nil
		}
	}
	return nil, fmt.Errorf("the key in %s does not match internal/catalog/key.go (dev key: build with -tags devkey)", path)
}

// WriteDir replaces out/apps with apps and writes a freshly signed index.json + index.sig
// (generated_at = now, so Boxes accept it as newer).
func WriteDir(out string, priv ed25519.PrivateKey, apps []*App) error {
	os.RemoveAll(filepath.Join(out, "apps"))
	if err := os.MkdirAll(filepath.Join(out, "apps"), 0o755); err != nil {
		return err
	}
	idx := Index{Version: 1, GeneratedAt: time.Now().Unix()}
	for _, a := range apps {
		b, err := json.MarshalIndent(a, "", " ")
		if err != nil {
			return err
		}
		file := "apps/" + a.ID + ".json"
		if err := os.WriteFile(filepath.Join(out, file), b, 0o644); err != nil {
			return err
		}
		idx.Apps = append(idx.Apps, IndexEntry{Summary: a.Summary, File: file, SHA256: FileHash(b)})
	}
	stacks, err := readStacks(out)
	if err != nil {
		return err
	}
	have := map[string]bool{}
	for _, a := range apps {
		have[a.ID] = true
	}
	stacks = keepKnown(stacks, have)
	if err := ValidateStacks(stacks, func(id string) bool { return have[id] }); err != nil {
		return err
	}
	idx.Stacks = stacks
	raw, err := json.Marshal(idx)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(out, "index.json"), raw, 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(out, "index.sig"), []byte(Sign(priv, raw)+"\n"), 0o644)
}
