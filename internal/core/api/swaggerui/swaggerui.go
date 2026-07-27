// Package swaggerui embeds the Swagger UI static assets that /api/docs needs.
//
// They are vendored, not fetched from a CDN. Azimuthal is a self-hosted
// product: a deployment on an isolated network is the normal case, not the
// exception, and a docs page whose stylesheet and two script bundles come from
// unpkg.com is a blank screen there with no explanation. It is also a supply
// chain nobody chose — a `@5` tag on a public CDN resolves to whatever was
// published this morning, executing in the browser of an authenticated
// administrator, on the origin that holds their session.
//
// Version 5.32.11, taken from the swagger-ui-dist npm package. Apache 2.0
// (assets/LICENSE), and the bundles' own third-party notices are alongside it.
//
// To update: fetch the release, replace the four files in assets/, and update
// the version in this comment and in Version below.
package swaggerui

import (
	"embed"
	"io/fs"
	"net/http"
)

// Version is the vendored swagger-ui-dist release. Reported by the handler so
// an operator can tell what is being served without unpacking the binary.
const Version = "5.32.11"

//go:embed assets
var assets embed.FS

// FS returns the embedded asset tree, rooted so "swagger-ui.css" resolves.
func FS() fs.FS {
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		// Unreachable: the directory is embedded at compile time, so a
		// failure here means the binary was built without it.
		panic("swaggerui: embedded assets missing: " + err.Error())
	}
	return sub
}

// Handler serves the embedded assets. It is mounted under a prefix by the
// caller, which strips it.
func Handler() http.Handler {
	return http.FileServer(http.FS(FS()))
}
