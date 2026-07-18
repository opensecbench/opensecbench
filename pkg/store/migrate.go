// Package store owns the SQLite-backed structured data and its schema migrations.
//
// This phase (P0) implements migration loading and ordering. Opening a database and applying
// migrations requires a SQL driver and is wired in P1 alongside the domain schema.
package store

import (
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

// Migration is one ordered schema change, loaded from a file named NNNN_name.sql.
type Migration struct {
	Version int
	Name    string
	SQL     string
}

// LoadMigrations reads every *.sql file from fsys, parses and orders them, and verifies the
// versions are contiguous starting at 1. It returns an error on malformed names, duplicate
// versions, or gaps — a migration set must be unambiguous.
func LoadMigrations(fsys fs.FS) ([]Migration, error) {
	names, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return nil, err
	}

	migrations := make([]Migration, 0, len(names))
	seen := make(map[int]bool, len(names))
	for _, name := range names {
		version, label, err := parseMigrationName(name)
		if err != nil {
			return nil, err
		}
		if seen[version] {
			return nil, fmt.Errorf("store: duplicate migration version %04d", version)
		}
		seen[version] = true

		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		migrations = append(migrations, Migration{Version: version, Name: label, SQL: string(body)})
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	for i, m := range migrations {
		if want := i + 1; m.Version != want {
			return nil, fmt.Errorf("store: migrations must be contiguous from 1; found %04d where %04d expected", m.Version, want)
		}
	}
	return migrations, nil
}

// parseMigrationName splits "NNNN_name.sql" into its numeric version and label.
func parseMigrationName(name string) (version int, label string, err error) {
	base := strings.TrimSuffix(name, ".sql")
	sep := strings.IndexByte(base, '_')
	if sep <= 0 {
		return 0, "", fmt.Errorf("store: bad migration filename %q (want NNNN_name.sql)", name)
	}
	version, err = strconv.Atoi(base[:sep])
	if err != nil {
		return 0, "", fmt.Errorf("store: bad migration version in %q: %w", name, err)
	}
	return version, base[sep+1:], nil
}
