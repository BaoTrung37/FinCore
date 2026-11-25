package migrations

import "embed"

// FS holds embedded SQL migration files.
//
//go:embed *.sql
var FS embed.FS
