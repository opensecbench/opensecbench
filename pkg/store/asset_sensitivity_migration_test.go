package store

import (
	"context"
	"io/fs"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/model"
)

// assetLinkPreservationCase applies fsys up to (but excluding) cutoff, seeds an asset plus rows in both
// child tables that reference it, then applies the rest — proving the assets-table rebuild that widens
// the sensitivity CHECK to include 'internal' keeps tasks.asset_id / playbook_runs.asset_id intact (the
// DROP would otherwise SET NULL them, since foreign_keys can't be toggled mid-migration) and that the
// widened CHECK then accepts 'internal'.
func assetLinkPreservationCase(t *testing.T, fsys fs.FS, cutoff int) {
	t.Helper()
	db := openTestDB(t)
	ctx := context.Background()
	all, err := LoadMigrations(fsys)
	if err != nil {
		t.Fatal(err)
	}

	var pre []Migration
	for _, m := range all {
		if m.Version < cutoff {
			pre = append(pre, m)
		}
	}
	if _, err := db.Apply(pre); err != nil {
		t.Fatal(err)
	}

	proj, _ := db.CreateProject(ctx, NewProject{Name: "P"})
	app, _ := db.CreateApplication(ctx, proj.ID, "app")
	asset, err := db.CreateAsset(ctx, NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: "/work/x", Sensitivity: model.SensitivityPrivate})
	if err != nil {
		t.Fatal(err)
	}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO tasks (id, capability_id, capability_version, asset_id, actor, runner, created_at) VALUES ('t1','cap','1',?,'human','local',?)`,
		asset.ID, ts); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO playbook_runs (id, playbook_id, asset_id, created_at) VALUES ('pr1','pb',?,?)`,
		asset.ID, ts); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Apply(all); err != nil {
		t.Fatalf("apply sensitivity migration: %v", err)
	}

	if got, err := db.GetAsset(ctx, asset.ID); err != nil || got.Sensitivity != model.SensitivityPrivate {
		t.Fatalf("asset lost or altered after rebuild: %+v err=%v", got, err)
	}
	var taskAsset, prunAsset string
	if err := db.QueryRowContext(ctx, `SELECT asset_id FROM tasks WHERE id='t1'`).Scan(&taskAsset); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(ctx, `SELECT asset_id FROM playbook_runs WHERE id='pr1'`).Scan(&prunAsset); err != nil {
		t.Fatal(err)
	}
	if taskAsset != asset.ID {
		t.Fatalf("task.asset_id = %q, want %q (link nulled by rebuild)", taskAsset, asset.ID)
	}
	if prunAsset != asset.ID {
		t.Fatalf("playbook_run.asset_id = %q, want %q (link nulled by rebuild)", prunAsset, asset.ID)
	}
	if _, err := db.UpdateAssetSensitivity(ctx, asset.ID, model.SensitivityInternal); err != nil {
		t.Fatalf("internal sensitivity rejected after migration: %v", err)
	}
}

// The project set (migrations/project/) is what the running app applies to each project.db — this is the
// migration that actually reaches users editing an asset's sensitivity.
func TestProjectMigrationAssetInternal(t *testing.T) {
	assetLinkPreservationCase(t, migrations.Project(), 6)
}

// The legacy single-database set (migrations/*.sql) still backs the store/analyst test fixtures.
func TestLegacyMigrationAssetInternal(t *testing.T) {
	assetLinkPreservationCase(t, migrations.FS, 53)
}
