// Package web embeds the built frontend assets into the Go binary.
package web

import "embed"

// DistFS contains the compiled frontend assets from web/dist/.
// The Vite build outputs to this directory, and the Go binary
// serves them as a SPA with fallback to index.html.
//
//go:embed all:dist
var DistFS embed.FS
