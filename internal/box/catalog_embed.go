package box

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	catalogdata "github.com/NexoVirt/nexovirt-box/catalog"
	"github.com/NexoVirt/nexovirt-box/internal/catalog"
)

// The built-in catalog, signed like a synced one (see docs/app-store.md).
var builtinCatalogFS fs.FS = catalogdata.FS

// loadCatalog returns the verified catalog to use: a synced one from webState/catalog when it
// verifies and is not older than the built-in one, else the built-in one.
func loadCatalog(webState string) (*catalog.Catalog, string, error) {
	builtin, err := catalog.Load(builtinCatalogFS, catalog.PublicKeys)
	if err != nil {
		return nil, "", err
	}
	if webState != "" {
		dir := filepath.Join(webState, "catalog")
		if _, err := os.Stat(filepath.Join(dir, "index.json")); err == nil {
			if synced, err := catalog.Load(os.DirFS(dir), catalog.PublicKeys); err == nil && synced.Index.GeneratedAt >= builtin.Index.GeneratedAt {
				return synced, "synced", nil
			}
		}
	}
	return builtin, "built-in", nil
}

func goarch() string { return runtime.GOARCH }
