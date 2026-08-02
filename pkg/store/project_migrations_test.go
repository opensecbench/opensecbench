package store

import (
	"context"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
)

// TestProjectMigrationSetApplies exercises the real per-project migration set (migrations/project/*.sql)
// end to end — coverage the combined-schema test fixtures don't provide. It guards two properties of the
// assets table after the sensitivity-registry rebuild (0017): the derived `ecosystems` column survives the
// rebuild, and the fixed CHECK on `sensitivity` is gone so any registry level is a valid value.
func TestProjectMigrationSetApplies(t *testing.T) {
	m, err := OpenManager(t.TempDir(), migrations.Global(), migrations.Project())
	if err != nil {
		t.Fatalf("open manager: %v", err)
	}
	pdb, err := m.Project("p1")
	if err != nil {
		t.Fatalf("open project db: %v", err)
	}
	ctx := context.Background()

	var cols string
	if err := pdb.QueryRowContext(ctx, `SELECT group_concat(name) FROM pragma_table_info('assets')`).Scan(&cols); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cols, "ecosystems") {
		t.Fatalf("assets lost the ecosystems column after project migrations: %s", cols)
	}

	var ddl string
	if err := pdb.QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE type='table' AND name='assets'`).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(ddl, "CHECK (sensitivity") {
		t.Fatalf("assets.sensitivity should have no CHECK after the registry rebuild:\n%s", ddl)
	}
}
