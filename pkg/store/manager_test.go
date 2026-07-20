package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
)

func openManager(t *testing.T) *Manager {
	t.Helper()
	m, err := OpenManager(t.TempDir(), migrations.Global(), migrations.Project())
	if err != nil {
		t.Fatalf("OpenManager: %v", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	return m
}

// TestManagerSchemasApply proves the two consolidated schemas (ADR-0049) are valid SQL under foreign-key
// enforcement: a global-only table lives in global.db and a project-only table lives in project.db, and
// neither leaks into the other database.
func TestManagerSchemasApply(t *testing.T) {
	m := openManager(t)

	// A global table exists in global.db but not in a project.db.
	if !hasTable(t, m.Global(), "kb_entries") {
		t.Error("global.db missing kb_entries")
	}
	if !hasTable(t, m.Global(), "project_index") {
		t.Error("global.db missing project_index")
	}
	pdb, err := m.Project("proj-a")
	if err != nil {
		t.Fatalf("Project: %v", err)
	}
	if hasTable(t, pdb, "kb_entries") {
		t.Error("project.db unexpectedly has global table kb_entries")
	}
	// A project table exists in project.db but not in global.db.
	if !hasTable(t, pdb, "findings") {
		t.Error("project.db missing findings")
	}
	if hasTable(t, m.Global(), "findings") {
		t.Error("global.db unexpectedly has project table findings")
	}
}

// TestProjectsAreIsolated writes a row in one project and confirms a second project's database cannot see
// it — the databases are physically separate files.
func TestProjectsAreIsolated(t *testing.T) {
	m := openManager(t)
	a, err := m.Project("proj-a")
	if err != nil {
		t.Fatal(err)
	}
	b, err := m.Project("proj-b")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Exec(`INSERT INTO settings_probe DEFAULT VALUES`); err == nil {
		t.Fatal("expected no settings_probe table in project.db")
	}
	// Use a real project table.
	if _, err := a.Exec(
		`INSERT INTO findings (id, title, created_at, updated_at) VALUES ('f1', 'only in A', '', '')`); err != nil {
		t.Fatalf("insert into A: %v", err)
	}
	if got := countRows(t, a, "findings"); got != 1 {
		t.Errorf("A findings = %d, want 1", got)
	}
	if got := countRows(t, b, "findings"); got != 0 {
		t.Errorf("B findings = %d, want 0 (isolation breach)", got)
	}
}

// TestPurgeProjectRemovesEverything confirms purge deletes the whole directory (db + cas + workspace) and
// drops the global index row.
func TestPurgeProjectRemovesEverything(t *testing.T) {
	ctx := context.Background()
	m := openManager(t)

	if _, err := m.Project("doomed"); err != nil {
		t.Fatal(err)
	}
	if err := m.Global().UpsertProjectIndex(ctx, "doomed", "Doomed Engagement", "active"); err != nil {
		t.Fatal(err)
	}
	// Simulate CAS + workspace content inside the project dir.
	casFile := filepath.Join(m.ProjectCASDir("doomed"), "ab", "blob")
	if err := os.MkdirAll(filepath.Dir(casFile), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(casFile, []byte("evidence"), 0o600); err != nil {
		t.Fatal(err)
	}

	rows, err := m.Global().ListProjectIndex(ctx)
	if err != nil || len(rows) != 1 {
		t.Fatalf("index before purge = %v (err %v), want 1 row", rows, err)
	}

	if err := m.PurgeProject(ctx, "doomed"); err != nil {
		t.Fatalf("PurgeProject: %v", err)
	}
	if _, err := os.Stat(m.ProjectDir("doomed")); !os.IsNotExist(err) {
		t.Errorf("project dir still exists after purge: %v", err)
	}
	rows, err = m.Global().ListProjectIndex(ctx)
	if err != nil || len(rows) != 0 {
		t.Errorf("index after purge = %v (err %v), want 0 rows", rows, err)
	}
}

func hasTable(t *testing.T, db *DB, name string) bool {
	t.Helper()
	var n int
	if err := db.QueryRow(
		`SELECT count(*) FROM sqlite_master WHERE type='table' AND name=?`, name).Scan(&n); err != nil {
		t.Fatalf("hasTable(%s): %v", name, err)
	}
	return n > 0
}

func countRows(t *testing.T, db *DB, table string) int {
	t.Helper()
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n); err != nil {
		t.Fatalf("countRows(%s): %v", table, err)
	}
	return n
}
