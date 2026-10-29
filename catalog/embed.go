// Package catalog embeds the built-in signed app catalog (see docs/app-store.md).
package catalog

import "embed"

// FS holds index.json, index.sig, apps/ and LICENSES/ at its root.
//
//go:embed index.json index.sig all:apps all:LICENSES
var FS embed.FS
