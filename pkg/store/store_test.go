package store

import (
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
)

// openTestDB opens a fresh on-disk database in a temp dir.
func openTestDB(t *testing.T) *DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestApplyMigrationsIsIdempotent(t *testing.T) {
	db := openTestDB(t)
	ms, err := LoadMigrations(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}

	applied, err := db.Apply(ms)
	if err != nil {
		t.Fatal(err)
	}
	if applied != len(ms) {
		t.Fatalf("applied %d migrations, want %d", applied, len(ms))
	}

	v, err := db.Version()
	if err != nil {
		t.Fatal(err)
	}
	if v != ms[len(ms)-1].Version {
		t.Fatalf("version = %d, want %d", v, ms[len(ms)-1].Version)
	}

	// Re-applying applies nothing new.
	again, err := db.Apply(ms)
	if err != nil {
		t.Fatal(err)
	}
	if again != 0 {
		t.Fatalf("re-apply applied %d migrations, want 0", again)
	}
}

func TestForeignKeysEnforced(t *testing.T) {
	db := openTestDB(t)
	var on int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&on); err != nil {
		t.Fatal(err)
	}
	if on != 1 {
		t.Fatal("foreign key enforcement is not enabled")
	}
}
