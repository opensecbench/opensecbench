package store

import (
	"testing"
	"testing/fstest"

	"github.com/opensecbench/opensecbench/migrations"
)

func TestLoadEmbeddedMigrations(t *testing.T) {
	ms, err := LoadMigrations(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) == 0 {
		t.Fatal("no embedded migrations loaded")
	}
	if ms[0].Version != 1 || ms[0].Name != "init" {
		t.Fatalf("first migration = %04d_%s, want 0001_init", ms[0].Version, ms[0].Name)
	}
}

func TestLoadMigrationsOrdersAndValidates(t *testing.T) {
	ok := fstest.MapFS{
		"0002_targets.sql": {Data: []byte("-- b")},
		"0001_init.sql":    {Data: []byte("-- a")},
	}
	ms, err := LoadMigrations(ok)
	if err != nil {
		t.Fatal(err)
	}
	if len(ms) != 2 || ms[0].Version != 1 || ms[1].Version != 2 {
		t.Fatalf("unexpected order: %+v", ms)
	}

	gap := fstest.MapFS{
		"0001_init.sql":    {Data: []byte("-- a")},
		"0003_targets.sql": {Data: []byte("-- c")},
	}
	if _, err := LoadMigrations(gap); err == nil {
		t.Fatal("expected error for non-contiguous versions")
	}

	dup := fstest.MapFS{
		"0001_init.sql": {Data: []byte("-- a")},
		"0001_dup.sql":  {Data: []byte("-- a2")},
	}
	if _, err := LoadMigrations(dup); err == nil {
		t.Fatal("expected error for duplicate version")
	}

	bad := fstest.MapFS{"init.sql": {Data: []byte("-- x")}}
	if _, err := LoadMigrations(bad); err == nil {
		t.Fatal("expected error for malformed filename")
	}
}
