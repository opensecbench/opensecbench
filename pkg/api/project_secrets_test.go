package api

import (
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/secret"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// TestDLPSecretValuesUnionsProjectSecrets proves the DLP egress redaction set covers BOTH the global
// vault and each project's own vault (ADR-0011/0049), so a project-scoped secret value cannot leak to an
// external LLM. It exercises the real two-tier storage (global.db + per-project project.db) rather than
// the combined test backing, since the union walks per-project directories.
func TestDLPSecretValuesUnionsProjectSecrets(t *testing.T) {
	dir := t.TempDir()
	mgr, err := store.OpenManager(dir, migrations.Global(), migrations.Project())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mgr.Close() })
	ctx := context.Background()

	// Global vault + a global secret.
	gkey := make([]byte, secret.KeySize)
	_, _ = rand.Read(gkey)
	gvault, err := secret.NewVault(gkey)
	if err != nil {
		t.Fatal(err)
	}
	gsealed, _ := gvault.Seal([]byte("GLOBAL-SECRET-VALUE"))
	if _, err := mgr.Global().SetSecret(ctx, "global_token", gsealed); err != nil {
		t.Fatal(err)
	}

	// A project with its own vault (key beside its project.db) + a project secret.
	proj, err := mgr.CreateProject(ctx, store.NewProject{Name: "Engagement A"})
	if err != nil {
		t.Fatal(err)
	}
	prov := secret.NewProvider()
	pvault, err := prov.For(mgr.ProjectDir(proj.ID))
	if err != nil {
		t.Fatal(err)
	}
	psealed, _ := pvault.Seal([]byte("PROJECT-SECRET-VALUE"))
	pdb, err := mgr.Project(proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pdb.SetSecret(ctx, "project_token", psealed); err != nil {
		t.Fatal(err)
	}

	// A second project with NO secrets must be skipped (and must not get a vault key materialized).
	empty, err := mgr.CreateProject(ctx, store.NewProject{Name: "Engagement B"})
	if err != nil {
		t.Fatal(err)
	}

	s := New(Deps{Store: mgr, Vault: gvault, VaultProvider: prov})
	got := s.dlpSecretValues(ctx)

	if got["GLOBAL-SECRET-VALUE"] != "global_token" {
		t.Errorf("global secret not in redaction set: %v", got)
	}
	if got["PROJECT-SECRET-VALUE"] != proj.ID+"/project_token" {
		t.Errorf("project secret not in redaction set (label %q): %v", got["PROJECT-SECRET-VALUE"], got)
	}
	if _, err := os.Stat(filepath.Join(mgr.ProjectDir(empty.ID), "vault.key")); err == nil {
		t.Errorf("empty project should not have a vault key materialized by the DLP scan")
	}
}
