package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestConnectorCRUD(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	c, err := db.CreateConnector(ctx, model.Connector{Name: "DefectDojo (prod)", Type: "defectdojo", BaseURL: "https://dd.local", Credential: "dd_token"})
	if err != nil {
		t.Fatal(err)
	}
	if c.ID == "" || c.Type != "defectdojo" {
		t.Fatalf("connector not stored: %+v", c)
	}
	got, err := db.GetConnector(ctx, c.ID)
	if err != nil || got.BaseURL != "https://dd.local" || got.Credential != "dd_token" {
		t.Fatalf("GetConnector = %+v err=%v", got, err)
	}
	if list, _ := db.ListConnectors(ctx); len(list) != 1 {
		t.Fatalf("list = %d, want 1", len(list))
	}
	if err := db.DeleteConnector(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetConnector(ctx, c.ID); err != ErrNotFound {
		t.Fatalf("after delete = %v, want ErrNotFound", err)
	}
}

func TestBindingCRUD(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "p"})
	conn, _ := db.CreateConnector(ctx, model.Connector{Name: "DD", Type: "defectdojo"})

	b, err := db.SetBinding(ctx, model.IntegrationBinding{ProjectID: proj.ID, ConnectorID: conn.ID, ProjectKey: "42"})
	if err != nil || b.ID == "" || b.ProjectKey != "42" {
		t.Fatalf("binding not stored: %+v err=%v", b, err)
	}
	// Upsert on (project, connector) updates the project_key in place.
	b2, err := db.SetBinding(ctx, model.IntegrationBinding{ProjectID: proj.ID, ConnectorID: conn.ID, ProjectKey: "7"})
	if err != nil || b2.ID != b.ID || b2.ProjectKey != "7" {
		t.Fatalf("upsert should update in place: %+v err=%v", b2, err)
	}
	if list, _ := db.ListBindings(ctx, proj.ID); len(list) != 1 {
		t.Fatalf("list = %d, want 1", len(list))
	}
	if err := db.DeleteBinding(ctx, proj.ID, conn.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetBinding(ctx, proj.ID, conn.ID); err != ErrNotFound {
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
