// Package migrations embeds the ordered SQL schema migrations applied by pkg/store.
package migrations

import (
	"embed"
	"io/fs"
)

// FS holds the legacy single-database migration files, named NNNN_name.sql and applied in
// ascending order. Retained until the two-tier storage layout (ADR-0049) fully lands.
//
//go:embed *.sql
var FS embed.FS

// domainFS holds the two-tier schema sets (ADR-0049): global/*.sql applied to global.db and
// project/*.sql applied to each per-project database.
//
//go:embed global/*.sql project/*.sql
var domainFS embed.FS

// Global returns the migration set for the instance-wide global.db.
func Global() fs.FS { return must(fs.Sub(domainFS, "global")) }

// Project returns the migration set for a per-project project.db.
func Project() fs.FS { return must(fs.Sub(domainFS, "project")) }

func must(f fs.FS, err error) fs.FS {
	if err != nil {
		panic(err)
	}
	return f
}
