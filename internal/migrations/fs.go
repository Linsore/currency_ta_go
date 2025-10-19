package migrations

import "embed"

// Embed all .sql files in this folder.
//go:embed *.sql
var Files embed.FS
