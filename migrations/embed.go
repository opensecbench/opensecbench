// Package migrations embeds the ordered SQL schema migrations applied by pkg/store.
package migrations

import "embed"

// FS holds the migration files, named NNNN_name.sql and applied in ascending order.
//
//go:embed *.sql
var FS embed.FS
