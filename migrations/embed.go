// Package migrations embeds the SQL migrations so the binary is self-contained.
package migrations

import "embed"

// FS contains every .sql migration in this directory.
//
//go:embed *.sql
var FS embed.FS
