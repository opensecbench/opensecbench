package store

import (
	"context"
	"errors"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
)

func migratedDB(t *testing.T) *DB {
	t.Helper()
	db := openTestDB(t)
	ms, err := LoadMigrations(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Apply(ms); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestProjectLifecycle(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	org, err := db.CreateOrganization(ctx, "AcmeCorp")
	if err != nil {
		t.Fatal(err)
	}
	tgt, err := db.CreateTarget(ctx, "Payments Platform", "core money movement", &org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if tgt.OrganizationID == nil || *tgt.OrganizationID != org.ID {
		t.Fatal("target did not retain its organization")
	}

	p, err := db.CreateProject(ctx, NewProject{
		Name:           "Q3 API Assessment",
		OrganizationID: &org.ID,
		TargetIDs:      []string{tgt.ID},
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.Status != "active" {
		t.Fatalf("new project status = %q, want active", p.Status)
	}

	got, err := db.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "Q3 API Assessment" {
		t.Fatalf("name = %q", got.Name)
	}
	if len(got.TargetIDs) != 1 || got.TargetIDs[0] != tgt.ID {
		t.Fatalf("target links = %v, want [%s]", got.TargetIDs, tgt.ID)
	}
	if got.OrganizationID == nil || *got.OrganizationID != org.ID {
		t.Fatal("project did not retain its organization")
	}

	list, err := db.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("listed %d projects, want 1", len(list))
	}
}

func TestCreateProjectRejectsUnknownTarget(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	// Foreign-key enforcement must reject a link to a nonexistent target, and the failed
	// transaction must leave no project behind.
	if _, err := db.CreateProject(ctx, NewProject{Name: "bad", TargetIDs: []string{"does-not-exist"}}); err == nil {
		t.Fatal("expected FK error for unknown target")
	}
	list, err := db.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("failed create leaked a project: %d rows", len(list))
	}
}

func TestDeleteProject(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	p, err := db.CreateProject(ctx, NewProject{Name: "temp"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteProject(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetProject(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetProject after delete = %v, want ErrNotFound", err)
	}
	if err := db.DeleteProject(ctx, p.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}
