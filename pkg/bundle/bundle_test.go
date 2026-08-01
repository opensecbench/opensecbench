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
	_, _ = db.AddScopeEntry(ctx, proj.ID, "domain", "acme.com", "allow")

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

	blob, err := Export(ctx, store.NewCombinedManager(src), srcBlobs, projID, "correct horse battery staple", false)
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
	blob, _ := Export(ctx, store.NewCombinedManager(src), srcBlobs, projID, "right", false)

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

// seedFull adds full-fidelity working state to an existing project (ADR-0060): an Analyst thread +
// message, an investigation (linked to the thread) on a fresh observation, an HTTP exchange with a
// response, a report + its CAS blob, a context note + its CAS blob, methodology adoption + coverage,
// and an engagement record with a contact + test account.
func seedFull(t *testing.T, db *store.DB, blobs *cas.Store, projID string) {
	t.Helper()
	ctx := context.Background()

	thread, _ := db.CreateThread(ctx, store.NewThread{ProjectID: &projID, Title: "Recon chat", Provider: "mock", AgentType: "generalist"})
	_, _ = db.AppendMessage(ctx, thread.ID, "user", "what does the login flow look like?")

	obs, _ := db.CreateObservation(ctx, model.Observation{ProjectID: &projID, Origin: model.OriginHuman, Title: "Weak TLS ciphers", Severity: "medium"})
	inv, _ := db.CreateInvestigation(ctx, model.Investigation{ProjectID: projID, ObservationID: obs.ID, Title: "Validate weak TLS", Status: model.InvestigationOpen})
	_ = db.SetInvestigationThread(ctx, inv.ID, thread.ID)

	ex, _ := db.CreateExchange(ctx, model.HTTPExchange{ProjectID: projID, Name: "login", Method: "POST", URL: "https://acme.com/login", RequestHeaders: "Content-Type: application/json", RequestBody: `{"u":"a"}`})
	_ = db.RecordResponse(ctx, ex.ID, 200, "Content-Type: text/html", "<html>ok</html>", 42, "")

	rd, _ := blobs.Put(bytes.NewReader([]byte("<html>REPORT</html>")))
	rart, _ := db.CreateArtifact(ctx, model.Artifact{SHA256: rd, Size: 19, Kind: model.ArtifactOutput, Name: "report.html", MediaType: "text/html"})
	_, _ = db.CreateReport(ctx, model.Report{ProjectID: projID, TemplateID: "technical", Format: "html", Title: "Technical Report", ArtifactID: rart.ID})

	nd, _ := blobs.Put(bytes.NewReader([]byte("kickoff notes: scope is storefront")))
	nart, _ := db.CreateArtifact(ctx, model.Artifact{SHA256: nd, Size: 34, Kind: model.ArtifactInput, Name: "notes.txt", MediaType: "text/plain"})
	_, _ = db.CreateContextItem(ctx, model.ContextItem{ProjectID: projID, Type: model.ContextNote, Name: "Kickoff notes", ArtifactID: nart.ID})

	_ = db.AdoptMethodology(ctx, projID, "owasp-asvs")
	_ = db.SetCoverage(ctx, projID, "V2.1.1", model.CoverageCovered, "verified manually")

	_, _ = db.SetEngagement(ctx, model.Engagement{
		ProjectID: projID, Objective: "Assess storefront", Environment: "staging", Authorized: true, Authorizer: "Jane Acme",
		Contacts:     []model.EngagementContact{{ProjectID: projID, Role: "primary", Name: "Jane", Email: "jane@acme.com"}},
		TestAccounts: []model.EngagementTestAccount{{ProjectID: projID, Role: "user", Username: "tester", SecretRef: "acct-pw"}},
	})
}

func TestExportImportFullRoundTrip(t *testing.T) {
	ctx := context.Background()
	src, srcBlobs := newStore(t)
	projID := seedProject(t, src, srcBlobs)
	seedFull(t, src, srcBlobs, projID)

	blob, err := Export(ctx, store.NewCombinedManager(src), srcBlobs, projID, "pw", true)
	if err != nil {
		t.Fatal(err)
	}

	dst, dstBlobs := newStore(t)
	newProjID, err := Import(ctx, store.NewCombinedManager(dst), cas.Fixed(dstBlobs), blob, "pw")
	if err != nil {
		t.Fatal(err)
	}

	// Analyst thread + message.
	threads, _ := dst.ListThreads(ctx)
	var thread *model.Thread
	for i := range threads {
		if threads[i].ProjectID != nil && *threads[i].ProjectID == newProjID {
			thread = &threads[i]
		}
	}
	if thread == nil || thread.Title != "Recon chat" {
		t.Fatalf("thread not restored: %+v", threads)
	}
	msgs, _ := dst.ListMessages(ctx, thread.ID)
	if len(msgs) != 1 || msgs[0].Content != "what does the login flow look like?" {
		t.Fatalf("messages not restored: %+v", msgs)
	}

	// Investigation, remapped onto the restored thread and observation.
	invs, _ := dst.ListInvestigationsByProject(ctx, newProjID)
	if len(invs) != 1 || invs[0].Title != "Validate weak TLS" {
		t.Fatalf("investigation not restored: %+v", invs)
	}
	if invs[0].ThreadID == nil || *invs[0].ThreadID != thread.ID {
		t.Fatalf("investigation lost its thread link: %+v", invs[0])
	}
	if _, err := dst.GetObservation(ctx, invs[0].ObservationID); err != nil {
		t.Fatalf("investigation observation not restored: %v", err)
	}

	// HTTP exchange with its response.
	exs, _ := dst.ListExchangesByProject(ctx, newProjID)
	if len(exs) != 1 || exs[0].URL != "https://acme.com/login" || exs[0].Status == nil || *exs[0].Status != 200 {
		t.Fatalf("exchange not restored: %+v", exs)
	}
	if exs[0].ResponseBody != "<html>ok</html>" {
		t.Fatalf("exchange response not restored: %+v", exs[0])
	}

	// Report + its rendered blob in the new CAS.
	reps, _ := dst.ListReportsByProject(ctx, newProjID)
	if len(reps) != 1 || reps[0].Title != "Technical Report" {
		t.Fatalf("report not restored: %+v", reps)
	}
	rart, _ := dst.GetArtifact(ctx, reps[0].ArtifactID)
	if rc, err := dstBlobs.Open(rart.SHA256); err != nil {
		t.Fatalf("report blob not in CAS: %v", err)
	} else {
		_ = rc.Close()
	}

	// Context note + its blob.
	items, _ := dst.ListContextItemsByProject(ctx, newProjID)
	if len(items) != 1 || items[0].Name != "Kickoff notes" || items[0].Type != model.ContextNote {
		t.Fatalf("context item not restored: %+v", items)
	}
	cart, _ := dst.GetArtifact(ctx, items[0].ArtifactID)
	if rc, err := dstBlobs.Open(cart.SHA256); err != nil {
		t.Fatalf("context blob not in CAS: %v", err)
	} else {
		_ = rc.Close()
	}

	// Methodology adoption + coverage.
	adopted, _ := dst.ListAdoptedMethodologies(ctx, newProjID)
	if len(adopted) != 1 || adopted[0] != "owasp-asvs" {
		t.Fatalf("methodology adoption not restored: %+v", adopted)
	}
	cov, _ := dst.ListCoverage(ctx, newProjID)
	if len(cov) != 1 || cov[0].Status != model.CoverageCovered {
		t.Fatalf("coverage not restored: %+v", cov)
	}

	// Engagement record + children.
	eng, err := dst.GetEngagement(ctx, newProjID)
	if err != nil {
		t.Fatalf("engagement not restored: %v", err)
	}
	if eng.Objective != "Assess storefront" || !eng.Authorized || len(eng.Contacts) != 1 || len(eng.TestAccounts) != 1 {
		t.Fatalf("engagement not faithfully restored: %+v", eng)
	}

	// A shareable export of the same project must NOT carry the full-fidelity state (ADR-0060: default
	// mode is the client deliverable, unchanged from ADR-0012).
	shareable, err := Export(ctx, store.NewCombinedManager(src), srcBlobs, projID, "pw", false)
	if err != nil {
		t.Fatal(err)
	}
	sd, err := open(shareable, "pw")
	if err != nil {
		t.Fatal(err)
	}
	if len(sd.Threads) != 0 || len(sd.Exchanges) != 0 || len(sd.Reports) != 0 || len(sd.ContextItems) != 0 || sd.Engagement != nil {
		t.Fatalf("shareable bundle leaked full-fidelity state: threads=%d exchanges=%d reports=%d ctx=%d eng=%v",
			len(sd.Threads), len(sd.Exchanges), len(sd.Reports), len(sd.ContextItems), sd.Engagement != nil)
	}
}
