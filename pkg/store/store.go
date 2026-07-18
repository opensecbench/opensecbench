// Package store owns the SQLite-backed structured data and its schema migrations.
package store

import (
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO)
)

// DB is the control-plane database handle.
type DB struct {
	*sql.DB
	auditMu sync.Mutex // serializes audit-chain appends
}

// Open opens (creating if needed) the SQLite database at path with foreign-key enforcement,
// a busy timeout, and WAL journaling — sensible defaults for a local single-writer workbench.
func Open(path string) (*DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)", path)
	sqldb, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := sqldb.Exec(`PRAGMA journal_mode = WAL`); err != nil {
		_ = sqldb.Close()
		return nil, fmt.Errorf("store: enable WAL: %w", err)
	}
	return &DB{DB: sqldb}, nil
}

// Apply runs every migration newer than the recorded schema version, each in its own
// transaction, and records it. It returns how many migrations were applied and is idempotent.
func (db *DB) Apply(migrations []Migration) (int, error) {
	if err := db.ensureMigrationsTable(); err != nil {
		return 0, err
	}
	current, err := db.currentVersion()
	if err != nil {
		return 0, err
	}

	applied := 0
	for _, m := range migrations {
		if m.Version <= current {
			continue
		}
		if err := db.applyOne(m); err != nil {
			return applied, fmt.Errorf("store: apply migration %04d_%s: %w", m.Version, m.Name, err)
		}
		applied++
	}
	return applied, nil
}

// Version returns the highest applied migration version (0 if none).
func (db *DB) Version() (int, error) {
	if err := db.ensureMigrationsTable(); err != nil {
		return 0, err
	}
	return db.currentVersion()
}

func (db *DB) ensureMigrationsTable() error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT NOT NULL,
		applied_at TEXT NOT NULL
	)`)
	return err
}

func (db *DB) currentVersion() (int, error) {
	var v sql.NullInt64
	if err := db.QueryRow(`SELECT MAX(version) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, err
	}
	return int(v.Int64), nil
}

func (db *DB) applyOne(m Migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.Exec(m.SQL); err != nil {
		return err
	}
	if _, err := tx.Exec(
		`INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		m.Version, m.Name, time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return err
	}
	return tx.Commit()
}
