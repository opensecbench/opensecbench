package store

import (
	"context"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/model"
)

// TestManagerSearchSplitSourcesKBFromGlobal reproduces the omni-search crash on split storage: the
// project database has no kb_entries table (KB lives in global.db), so running the search against a
// single project DB failed with "no such table: kb_entries". Manager.Search must instead source project
// entities from the project DB and KB from global.db, without crashing.
func TestManagerSearchSplitSourcesKBFromGlobal(t *testing.T) {
	ctx := context.Background()
	m, err := OpenManager(t.TempDir(), migrations.Global(), migrations.Project())
	if err != nil {
		t.Fatal(err)
	}

	proj, err := m.CreateProject(ctx, NewProject{Name: "acme-search"})
	if err != nil {
		t.Fatal(err)
	}

	// A finding lives in the project database — which has no kb_entries (the bug's precondition).
	pdb, err := m.Project(proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pdb.CreateFinding(ctx, NewFinding{Title: "SQL injection in orders", Severity: "high"}); err != nil {
		t.Fatal(err)
	}

	// A KB entry lives only in global.db.
	if _, err := m.Global().CreateKBEntry(ctx, model.KBEntry{Scope: "global", Kind: "gotcha", Title: "SQL escaping gotcha"}); err != nil {
		t.Fatal(err)
	}

	// Pre-fix this crashed with "no such table: kb_entries".
	res, err := m.Search(ctx, proj.ID, "SQL", 25)
	if err != nil {
		t.Fatalf("split search crashed: %v", err)
	}

	var gotFinding, gotKB bool
	for _, r := range res {
		if r.Kind == "finding" && strings.Contains(r.Title, "SQL injection") {
			gotFinding = true
		}
		if r.Kind == "kb" && strings.Contains(r.Title, "SQL escaping") {
			gotKB = true
		}
	}
	if !gotFinding {
		t.Errorf("project finding missing from split search: %+v", res)
	}
	if !gotKB {
		t.Errorf("global KB entry missing from split search (cross-db): %+v", res)
	}
}
