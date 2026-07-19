package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestIntegrationConfigCRUD(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "p"})

	c, err := db.SetIntegrationConfig(ctx, model.IntegrationConfig{
		ProjectID: proj.ID, Integration: "defectdojo", BaseURL: "https://dd.local", ProjectKey: "42", Credential: "dd_token",
	})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == "" || c.BaseURL != "https://dd.local" || c.Credential != "dd_token" {
		t.Fatalf("config not stored: %+v", c)
	}

	// Upsert on the unique (project, integration) key updates in place.
	c2, err := db.SetIntegrationConfig(ctx, model.IntegrationConfig{
		ProjectID: proj.ID, Integration: "defectdojo", BaseURL: "https://dd2.local", ProjectKey: "7", Credential: "dd_token2",
	})
	if err != nil {
		t.Fatal(err)
	}
	if c2.ID != c.ID || c2.BaseURL != "https://dd2.local" {
		t.Fatalf("upsert should update in place: %+v", c2)
	}

	got, err := db.GetIntegrationConfig(ctx, proj.ID, "defectdojo")
	if err != nil || got.ProjectKey != "7" {
		t.Fatalf("GetIntegrationConfig = %+v err=%v", got, err)
	}
	if list, _ := db.ListIntegrationConfigs(ctx, proj.ID); len(list) != 1 {
		t.Fatalf("list = %d, want 1", len(list))
	}
	if err := db.DeleteIntegrationConfig(ctx, proj.ID, "defectdojo"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetIntegrationConfig(ctx, proj.ID, "defectdojo"); err != ErrNotFound {
		t.Fatalf("after delete = %v, want ErrNotFound", err)
	}
}

func TestImportDedup(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "p"})
	obs, _ := db.CreateObservation(ctx, model.Observation{ProjectID: &proj.ID, Title: "x", Origin: model.OriginTool})

	if has, _ := db.HasImport(ctx, proj.ID, "defectdojo", "ext-1"); has {
		t.Fatal("should not be imported yet")
	}
	if err := db.RecordImport(ctx, proj.ID, "defectdojo", "ext-1", obs.ID); err != nil {
		t.Fatal(err)
	}
	if has, _ := db.HasImport(ctx, proj.ID, "defectdojo", "ext-1"); !has {
		t.Fatal("should be imported now")
	}
	// A second RecordImport for the same external id is ignored (idempotent).
	if err := db.RecordImport(ctx, proj.ID, "defectdojo", "ext-1", obs.ID); err != nil {
		t.Fatalf("re-record should be a no-op, got %v", err)
	}
}

// A task-less observation attached directly to a project appears in the project's list (integration pull).
func TestTaskLessObservationScopedToProject(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "p"})
	other, _ := db.CreateProject(ctx, NewProject{Name: "other"})

	if _, err := db.CreateObservation(ctx, model.Observation{
		ProjectID: &proj.ID, Title: "imported from DefectDojo", Severity: "high", Origin: model.OriginTool,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := db.ListObservationsByProject(ctx, proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Title != "imported from DefectDojo" || got[0].ProjectID == nil {
		t.Fatalf("project observations = %+v, want the direct one", got)
	}
	if other, _ := db.ListObservationsByProject(ctx, other.ID); len(other) != 0 {
		t.Fatalf("other project should see no observations, got %d", len(other))
	}
}
