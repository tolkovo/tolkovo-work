// Package migrations embeds the goose SQL migrations into the binary.
// Applied migrations are never edited: fixes go into a new file.
package migrations

import "embed"

// FS holds every NNNNN_name.sql migration.
//
//go:embed *.sql
var FS embed.FS
