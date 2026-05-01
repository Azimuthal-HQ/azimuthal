// Package migrations provides the embedded SQL migration files for Azimuthal.
// Goose reads from this embedded filesystem at startup via internal/db.Migrate.
package migrations

import "embed"

// FS contains all goose SQL migration files embedded at build time.
//
//go:embed *.sql
var FS embed.FS
