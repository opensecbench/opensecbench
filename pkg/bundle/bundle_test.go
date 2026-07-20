package bundle

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/extension"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

func newStore(t *testing.T) (*store.DB, *cas.Store) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "db.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := store.LoadMigrations(migrations.FS)
	if _, err := db.Apply(ms); err != nil {
		t.Fatal(err)
	}
	blobs, err := cas.Open(filepath.Join(t.TempDir(), "cas"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, blobs
}

// seedProject builds a project with a target, app, evidence artifact, confirmed observation,
// a finding backed by it, and a KB entry. Returns the project id.
func seedProject(t *testing.T, db *store.DB, blobs *cas.Store) string {
	t.Helper()
	ctx := context.Background()

	target, _ := db.CreateTarget(ctx, "Acme Platform", "durable", nil)
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "Acme 2026", TargetIDs: []string{target.ID}})
	app, _ := db.CreateApplication(ctx, proj.ID, "Storefront")
	_, _ = db.AddScopeEntry(ctx, proj.ID, "domain", "acme.com")

	digest, _ := blobs.Put(bytes.NewReader([]byte("EVIDENCE: response headers here")))
	art, _ := db.CreateArtifact(ctx, model.Artifact{SHA256: digest, Size: 31, Kind: model.ArtifactInput, Name: "resp", MediaType: "text/plain"})
	obs, _ := db.CreateObservation(ctx, model.Observation{
		ArtifactID: &art.ID, Origin: model.OriginHuman, Title: "SQLi in /login", Location: "login.go:42", Severity: "high",
	})
	_ = db.ReviewObservation(ctx, obs.ID, model.ReviewConfirmed)

	f, _ := db.CreateFinding(ctx, store.NewFinding{
		ApplicationID: &app.ID, Title: "Auth bypass", Severity: "high", CWE: "CWE-89", ObservationIDs: []string{obs.ID},
	})
	_ = db.SetFindingStatus(ctx, f.ID, model.FindingConfirmed)

	_, _ = db.CreateKBEntry(ctx, model.KBEntry{TargetID: target.ID, Kind: model.KBAuth, Title: "SAML SSO via Okta"})
	return proj.ID
}

func TestExportImportRoundTrip(t *testing.T) {
	ctx := context.Background()
	src, srcBlobs := newStore(t)
	projID := seedProject(t, src, srcBlobs)

	blob, err := Export(ctx, store.NewCombinedManager(src), srcBlobs, projID, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(blob, []byte("Auth bypass")) {
		t.Fatal("bundle is not encrypted (plaintext finding title present)")
	}

	// Import into a fresh instance.
	dst, dstBlobs := newStore(t)
	newProjID, err := Import(ctx, store.NewCombinedManager(dst), cas.Fixed(dstBlobs), blob, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if newProjID == projID {
		t.Fatal("import should remap the project id")
	}

	// Fidelity: project, app, finding + evidence, KB all present in the destination.
	np, _ := dst.GetProject(ctx, newProjID)
	if np.Name != "Acme 2026" || len(np.TargetIDs) != 1 {
		t.Fatalf("project not restored: %+v", np)
	}
	apps, _ := dst.ListApplicationsByProject(ctx, newProjID)
	if len(apps) != 1 || apps[0].Name != "Storefront" {
		t.Fatalf("apps not restored: %+v", apps)
	}

	findings, _ := dst.ListFindings(ctx)
	if len(findings) != 1 {
		t.Fatalf("findings restored = %d, want 1", len(findings))
	}
	full, _ := dst.GetFinding(ctx, findings[0].ID)
	if full.Title != "Auth bypass" || full.Status != model.FindingConfirmed || full.CWE != "CWE-89" {
		t.Fatalf("finding not faithfully restored: %+v", full)
	}
	if len(full.ObservationIDs) != 1 {
		t.Fatalf("finding lost its evidence link")
	}
	obs, _ := dst.GetObservation(ctx, full.ObservationIDs[0])
	if obs.ArtifactID == nil {
		t.Fatal("observation lost its artifact")
	}
	art, _ := dst.GetArtifact(ctx, *obs.ArtifactID)

	// Evidence content hash is preserved exactly, and the blob is retrievable from the new CAS.
	rc, err := dstBlobs.Open(art.SHA256)
	if err != nil {
		t.Fatalf("evidence blob not in destination CAS: %v", err)
	}
	_ = rc.Close()

	kb, _ := dst.ListKBByProject(ctx, newProjID)
	if len(kb) != 1 || kb[0].Title != "SAML SSO via Okta" {
		t.Fatalf("KB not restored: %+v", kb)
	}

	// Re-import is safe: a second import yields a distinct project.
	again, err := Import(ctx, store.NewCombinedManager(dst), cas.Fixed(dstBlobs), blob, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if again == newProjID {
		t.Fatal("re-import should create a distinct project")
	}
}

func TestWrongPassphraseFails(t *testing.T) {
	ctx := context.Background()
	src, srcBlobs := newStore(t)
	projID := seedProject(t, src, srcBlobs)
	blob, _ := Export(ctx, store.NewCombinedManager(src), srcBlobs, projID, "right")

	dst, dstBlobs := newStore(t)
	if _, err := Import(ctx, store.NewCombinedManager(dst), cas.Fixed(dstBlobs), blob, "wrong"); err == nil {
		t.Fatal("import with wrong passphrase should fail")
	}
}

func TestBundleSignVerify(t *testing.T) {
	pub, priv, err := extension.GenerateKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	data := []byte("an encrypted bundle blob")
	sc, err := Sign(data, "acme", priv)
	if err != nil {
		t.Fatal(err)
	}
	if sc.Publisher != "acme" || sc.PublicKey != pub {
		t.Fatalf("sidecar wrong: %+v", sc)
	}
	if err := sc.Verify(data); err != nil {
		t.Fatalf("valid signature should verify: %v", err)
	}
	// Tampered bundle fails.
	if err := sc.Verify([]byte("tampered blob")); err == nil {
		t.Fatal("tampered bundle should fail verification")
	}
	// Round-trips through JSON.
	raw, _ := MarshalSidecar(sc)
	got, err := ParseSidecar(raw)
	if err != nil || got.Verify(data) != nil {
		t.Fatalf("sidecar json round trip failed: %v", err)
	}
}
