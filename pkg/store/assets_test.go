package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestApplicationAndAssetHierarchy(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	proj, err := db.CreateProject(ctx, NewProject{Name: "Engagement"})
	if err != nil {
		t.Fatal(err)
	}
	app, err := db.CreateApplication(ctx, proj.ID, "payments-api")
	if err != nil {
		t.Fatal(err)
	}
	if app.ProjectID != proj.ID {
		t.Fatal("application not linked to project")
	}

	// Sensitivity inferred from location.
	priv, err := db.CreateAsset(ctx, NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: "/work/acme/payments"})
	if err != nil {
		t.Fatal(err)
	}
	if priv.Sensitivity != model.SensitivityPrivate {
		t.Fatalf("sensitivity = %q, want private (default)", priv.Sensitivity)
	}
	oss, err := db.CreateAsset(ctx, NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: "/opt/oss/acme-sdk"})
	if err != nil {
		t.Fatal(err)
	}
	if oss.Sensitivity != model.SensitivityOpenSource {
		t.Fatalf("sensitivity = %q, want open_source (inferred from /oss/)", oss.Sensitivity)
	}

	// Explicit override wins.
	forced, err := db.CreateAsset(ctx, NewAsset{ApplicationID: app.ID, Type: model.AssetDocument, Location: "/oss/readme.md", Sensitivity: model.SensitivityPrivate})
	if err != nil {
		t.Fatal(err)
	}
	if forced.Sensitivity != model.SensitivityPrivate {
		t.Fatal("explicit sensitivity override was ignored")
	}

	assets, err := db.ListAssetsByApplication(ctx, app.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(assets) != 3 {
		t.Fatalf("listed %d assets, want 3", len(assets))
	}

	got, err := db.GetAsset(ctx, priv.ID)
	if err != nil || got.Location != "/work/acme/payments" {
		t.Fatalf("GetAsset failed: %+v err=%v", got, err)
	}
}

func TestCreateAssetRejectsBadType(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "e"})
	app, _ := db.CreateApplication(ctx, proj.ID, "a")
	if _, err := db.CreateAsset(ctx, NewAsset{ApplicationID: app.ID, Type: "bogus", Location: "/x"}); err == nil {
		t.Fatal("expected error for invalid asset type")
	}
}
