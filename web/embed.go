// Package web embeds the built UI (web/dist, produced by `npm run build`).
package web

import "embed"

// Dist holds the built UI under dist/.
//
//go:embed all:dist
var Dist embed.FS
