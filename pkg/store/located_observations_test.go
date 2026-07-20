package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// A SAST observation produced by a scan of a source_repo asset must resolve back to that asset, so the UI can
// offer click-to-file (ADR-0050). A network observation with no source asset resolves to an empty AssetID.
func TestListLocatedObservationsResolvesAsset(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	proj, _ := db.CreateProject(ctx, NewProject{Name: "eng"})
	app, _ := db.CreateApplication(ctx, proj.ID, "Store")
	asset, _ := db.CreateAsset(ctx, NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: "/work/repo"})

	// A scan task against that source asset, and a finding located in a file.
	task, err := db.CreateTask(ctx, NewTask{CapabilityID: "semgrep", ApplicationID: &app.ID, AssetID: &asset.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateObservation(ctx, model.Observation{
		TaskID: &task.ID, Title: "SQLi", Severity: "high", Location: "app/views.py:42",
	}); err != nil {
		t.Fatal(err)
	}
	// A network task (no asset) with a host:port location.
	netTask, _ := db.CreateTask(ctx, NewTask{CapabilityID: "nmap", ProjectID: &proj.ID})
	if _, err := db.CreateObservation(ctx, model.Observation{
		TaskID: &netTask.ID, ProjectID: &proj.ID, Title: "open port", Severity: "info", Location: "10.0.0.1:443/tcp",
	}); err != nil {
		t.Fatal(err)
	}

	got, err := db.ListLocatedObservationsByProject(ctx, proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	byTitle := map[string]LocatedObservation{}
	for _, o := range got {
		byTitle[o.Title] = o
	}
	if byTitle["SQLi"].AssetID != asset.ID {
		t.Errorf("SQLi observation resolved asset_id=%q, want %q", byTitle["SQLi"].AssetID, asset.ID)
	}
	if byTitle["SQLi"].Location != "app/views.py:42" {
		t.Errorf("location not preserved: %q", byTitle["SQLi"].Location)
	}
	if byTitle["open port"].AssetID != "" {
		t.Errorf("network observation should have no asset, got %q", byTitle["open port"].AssetID)
	}
}
