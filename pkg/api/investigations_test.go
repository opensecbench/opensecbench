package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
)

func TestRunInvestigation(t *testing.T) {
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	// A provider that just answers (no tool calls) so the seeded investigation advance completes.
	mock := &llm.MockProvider{Responses: []string{`{"answer":"Looks like a placeholder value — likely a false positive."}`}}
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs, Provider: mock}).Handler())
	t.Cleanup(func() { srv.Close() })

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
	db := storetest.New(t)
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db)}).Handler())
	t.Cleanup(func() { srv.Close() })

	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "p"})
	obs, _ := db.CreateObservation(ctx, model.Observation{ProjectID: &proj.ID, Title: "x", Origin: model.OriginTool})
	inv, _ := db.CreateInvestigation(ctx, model.Investigation{ProjectID: proj.ID, ObservationID: obs.ID, Title: "x"})

	var body map[string]any
	if code := postJSON(t, srv.URL+"/v1/investigations/"+inv.ID+"/run", `{}`, &body); code != http.StatusServiceUnavailable {
		t.Fatalf("run with no provider = %d, want 503", code)
	}
}

func TestDescribeSignals(t *testing.T) {
	// A reachable, traffic-confirmed-route, high-CVSS observation renders the decision context.
	got := describeSignals(map[string]string{
		"reachable": "true", "dataflow_source": "app/views.py:12",
		"exposed_route": "POST /query", "route_observed": "true", "security_severity": "9.8",
	})
	for _, want := range []string{"Signals:", "Reachable: yes", "app/views.py:12", "POST /query", "traffic-confirmed", "CVSS: 9.8"} {
		if !strings.Contains(got, want) {
			t.Fatalf("describeSignals missing %q in:\n%s", want, got)
		}
	}
	// An imported-but-uncalled dependency vuln flags the likely false positive.
	fp := describeSignals(map[string]string{"reachable": "false", "package": "libfoo", "fixed_version": "1.2.3"})
	if !strings.Contains(fp, "Reachable: no") || !strings.Contains(fp, "libfoo") || !strings.Contains(fp, "fixed in 1.2.3") {
		t.Fatalf("unreachable render wrong:\n%s", fp)
	}
	// Nothing worth stating → empty (no dangling block in the seed).
	if s := describeSignals(nil); s != "" {
		t.Fatalf("empty attrs should render nothing, got %q", s)
	}
	if s := describeSignals(map[string]string{"tool": "grype"}); s != "" {
		t.Fatalf("no decision-relevant attrs should render nothing, got %q", s)
	}
}
