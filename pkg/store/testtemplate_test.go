package store

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
)

// This mirrors pkg/store/storetest, which other packages use — but store's own (internal) tests can't
// import that subpackage without an import cycle, so the migrate-once-and-clone machinery is duplicated
// here. Replaying all migrations per test costs ~2s under -race; cloning a template built once is ~1ms.

var (
	tmplOnce sync.Once
	tmplPath string
	tmplErr  error
)

func buildStoreTemplate() {
	dir, err := os.MkdirTemp("", "osb-store-tmpl")
	if err != nil {
		tmplErr = err
		return
	}
	path := filepath.Join(dir, "template.db")
	db, err := Open(path)
	if err != nil {
		tmplErr = err
		return
	}
	ms, err := LoadMigrations(migrations.FS)
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

// migratedDB returns a fully-migrated DB cloned from a template built once per test process, so store
// method tests don't each replay every migration. Migration-mechanism tests still open an empty DB and
// apply migrations themselves (openTestDB + Apply) — they must exercise the real Apply path.
func migratedDB(t *testing.T) *DB {
	t.Helper()
	tmplOnce.Do(buildStoreTemplate)
	if tmplErr != nil {
		t.Fatalf("migratedDB: build template: %v", tmplErr)
	}
	dst := filepath.Join(t.TempDir(), "test.db")
	if err := copyTemplateFile(tmplPath, dst); err != nil {
		t.Fatalf("migratedDB: clone template: %v", err)
	}
	db, err := Open(dst)
	if err != nil {
		t.Fatalf("migratedDB: open clone: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func copyTemplateFile(src, dst string) error {
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
