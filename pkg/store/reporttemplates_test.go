package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestReportTemplateCRUD(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ms, _ := LoadMigrations(migrations.FS)
	if _, err := db.Apply(ms); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Missing required fields are rejected.
	if _, err := db.SaveReportTemplate(ctx, model.ReportTemplate{ID: "x", Title: "X"}); err == nil {
		t.Fatal("expected error saving template with empty md/html")
	}

	// Create.
	saved, err := db.SaveReportTemplate(ctx, model.ReportTemplate{
		ID: "exec-custom", Title: "Exec (custom)", Kind: "custom", Base: "executive",
		MD: "# {{.Project.Name}}", HTML: "<h1>{{.Project.Name}}</h1>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if saved.CreatedAt.IsZero() || !saved.UpdatedAt.Equal(saved.CreatedAt) {
		t.Fatalf("timestamps: created=%v updated=%v", saved.CreatedAt, saved.UpdatedAt)
	}

	// Upsert (same id) edits in place and preserves created_at while bumping updated_at.
	edited, err := db.SaveReportTemplate(ctx, model.ReportTemplate{
		ID: "exec-custom", Title: "Exec v2", Kind: "custom", MD: "# hi", HTML: "<h1>hi</h1>",
	})
	if err != nil {
		t.Fatal(err)
	}
	if edited.Title != "Exec v2" {
		t.Fatalf("title = %q, want Exec v2", edited.Title)
	}
	if !edited.CreatedAt.Equal(saved.CreatedAt) {
		t.Errorf("created_at changed on upsert: %v -> %v", saved.CreatedAt, edited.CreatedAt)
	}

	// UpdateReportTemplate on a missing id returns ErrNotFound (this is how built-ins stay immutable).
	if _, err := db.UpdateReportTemplate(ctx, model.ReportTemplate{ID: "nope", Title: "t", MD: "m", HTML: "h"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update missing = %v, want ErrNotFound", err)
	}

	// List + Get.
	list, err := db.ListReportTemplates(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %d (err %v), want 1", len(list), err)
	}
	got, err := db.GetReportTemplate(ctx, "exec-custom")
	if err != nil || got.HTML != "<h1>hi</h1>" {
		t.Fatalf("get = %+v (err %v)", got, err)
	}

	// Delete.
	if err := db.DeleteReportTemplate(ctx, "exec-custom"); err != nil {
		t.Fatal(err)
	}
	if err := db.DeleteReportTemplate(ctx, "exec-custom"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete = %v, want ErrNotFound", err)
	}
	if _, err := db.GetReportTemplate(ctx, "exec-custom"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete = %v, want ErrNotFound", err)
	}
}
