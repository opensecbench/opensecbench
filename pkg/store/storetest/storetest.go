// Package storetest gives tests a fully-migrated store.DB without paying to replay every migration.
//
// Applying the schema's migrations one transaction at a time costs ~120ms normally but ~2s under the
// race detector, and the suite opens a fresh DB in almost every test — so that single setup line, not any
// slow test, dominated CI wall time. Here we run the real migrations exactly once per test process into a
// template file, then hand each test a byte-for-byte copy. Tests still exercise the true production schema
// (no drift), but setup drops to a file copy.
package storetest

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/store"
)

var (
	once     sync.Once
	tmplPath string
	tmplErr  error
)

// buildTemplate migrates a throwaway DB once and leaves its file ready to clone. It checkpoints the WAL
// back into the main file and closes cleanly, so a copy of that single file is a complete database.
func buildTemplate() {
	dir, err := os.MkdirTemp("", "osb-store-template")
	if err != nil {
		tmplErr = err
		return
	}
	path := filepath.Join(dir, "template.db")
	db, err := store.Open(path)
	if err != nil {
		tmplErr = err
		return
	}
	ms, err := store.LoadMigrations(migrations.FS)
	if err != nil {
		_ = db.Close()
		tmplErr = err
		return
	}
	if _, err := db.Apply(ms); err != nil {
		_ = db.Close()
		tmplErr = err
		return
	}
	// Fold the WAL into the main file so copying just template.db captures the whole schema.
	if _, err := db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		_ = db.Close()
		tmplErr = err
		return
	}
	if err := db.Close(); err != nil {
		tmplErr = err
		return
	}
	tmplPath = path
}

// New returns a freshly-migrated store.DB, cloned from a process-wide template built once. The DB is
// closed automatically when the test ends.
func New(t testing.TB) *store.DB {
	t.Helper()
	once.Do(buildTemplate)
	if tmplErr != nil {
		t.Fatalf("storetest: build template: %v", tmplErr)
	}
	dst := filepath.Join(t.TempDir(), "test.db")
	if err := copyFile(tmplPath, dst); err != nil {
		t.Fatalf("storetest: clone template: %v", err)
	}
	db, err := store.Open(dst)
	if err != nil {
		t.Fatalf("storetest: open clone: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
