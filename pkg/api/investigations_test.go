package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

func TestRunInvestigation(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := store.LoadMigrations(migrations.FS)
	if _, err := db.Apply(ms); err != nil {
		t.Fatal(err)
	}
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	// A provider that just answers (no tool calls) so the seeded investigation advance completes.
	mock := &llm.MockProvider{Responses: []string{`{"answer":"Looks like a placeholder value — likely a false positive."}`}}
	srv := httptest.NewServer(New(Deps{Store: db, CAS: blobs, Provider: mock}).Handler())
	t.Cleanup(func() { srv.Close(); _ = db.Close() })

	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "p"})
	obs, _ := db.CreateObservation(ctx, model.Observation{
		ProjectID: &proj.ID, Title: "Slack secret (unverified)", Severity: "medium", Origin: model.OriginTool,
		RuleID: "trufflehog:Slack", Attributes: map[string]string{"verified": "false"},
	})
	inv, _ := db.CreateInvestigation(ctx, model.Investigation{ProjectID: proj.ID, ObservationID: obs.ID, Title: obs.Title})

	// Run it: a vuln-validator thread is created and the investigation moves to investigating.
	var out struct {
		Thread model.Thread `json:"thread"`
	}
	if code := postJSON(t, srv.URL+"/v1/investigations/"+inv.ID+"/run", `{}`, &out); code != http.StatusOK {
		t.Fatalf("run = %d", code)
	}
	if out.Thread.ID == "" || out.Thread.AgentType != "vuln-validator" {
		t.Fatalf("investigation thread = %+v, want a vuln-validator thread", out.Thread)
	}
	got, _ := db.GetInvestigation(ctx, inv.ID)
	if got.Status != model.InvestigationInvestigating || got.ThreadID == nil || *got.ThreadID != out.Thread.ID {
		t.Fatalf("after run = %+v, want investigating linked to the thread", got)
	}

	// It shows in the project's investigations list.
	list, _ := db.ListInvestigationsByProject(ctx, proj.ID)
	if len(list) != 1 {
		t.Fatalf("list = %d, want 1", len(list))
	}

	// Resolve it.
	if code := postJSON(t, srv.URL+"/v1/investigations/"+inv.ID+"/status", `{"status":"resolved"}`, nil); code != http.StatusNoContent {
		t.Fatalf("resolve = %d", code)
	}
	if got, _ := db.GetInvestigation(ctx, inv.ID); got.Status != model.InvestigationResolved {
		t.Fatalf("after resolve = %s", got.Status)
	}
}

func TestRunInvestigationNoProvider(t *testing.T) {
	db, _ := store.Open(filepath.Join(t.TempDir(), "t.db"))
	ms, _ := store.LoadMigrations(migrations.FS)
	_, _ = db.Apply(ms)
	srv := httptest.NewServer(New(Deps{Store: db}).Handler())
	t.Cleanup(func() { srv.Close(); _ = db.Close() })

	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "p"})
	obs, _ := db.CreateObservation(ctx, model.Observation{ProjectID: &proj.ID, Title: "x", Origin: model.OriginTool})
	inv, _ := db.CreateInvestigation(ctx, model.Investigation{ProjectID: proj.ID, ObservationID: obs.ID, Title: "x"})

	var body map[string]any
	if code := postJSON(t, srv.URL+"/v1/investigations/"+inv.ID+"/run", `{}`, &body); code != http.StatusServiceUnavailable {
		t.Fatalf("run with no provider = %d, want 503", code)
	}
}
