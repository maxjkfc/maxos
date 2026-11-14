// Package migrations embeds the goose SQL migrations. `maxos migrate` and
// internal/testpg apply them, and sqlc reads the same directory as the schema.
package migrations

import "embed"

// FS holds every *.sql file of this directory at its root.
//
//go:embed *.sql
var FS embed.FS
