package analyst

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/replay"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// seedProject creates a project and returns a deps bound to it (no engine/replay unless set).
func seedProject(t *testing.T) (*store.DB, string) {
	t.Helper()
	db := migratedStore(t)
	proj, err := db.CreateProject(context.Background(), store.NewProject{Name: "P"})
	if err != nil {
		t.Fatal(err)
	}
	return db, proj.ID
}

func TestExchangeToolsAreProjectScoped(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	other, _ := db.CreateProject(ctx, store.NewProject{Name: "Other"})

	mine, _ := db.CreateExchange(ctx, model.HTTPExchange{ProjectID: projectID, Origin: "proxy", Method: "GET", URL: "https://acme.test/a"})
	theirs, _ := db.CreateExchange(ctx, model.HTTPExchange{ProjectID: other.ID, Origin: "proxy", Method: "GET", URL: "https://other.test/b"})

	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: projectID})

	// list_exchanges only returns the current project's traffic.
	out, err := exec(ctx, agent.ToolCall{Tool: "list_exchanges", Args: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, mine.ID) || strings.Contains(out, theirs.ID) {
		t.Fatalf("list_exchanges leaked across projects: %s", out)
	}

	// get_exchange refuses to read another project's exchange.
	if _, err := exec(ctx, agent.ToolCall{Tool: "get_exchange", Args: map[string]any{"id": theirs.ID}}); err == nil {
		t.Fatal("get_exchange should refuse a cross-project id")
	}
	if _, err := exec(ctx, agent.ToolCall{Tool: "get_exchange", Args: map[string]any{"id": mine.ID}}); err != nil {
		t.Fatalf("get_exchange on own project failed: %v", err)
	}
}

func TestCoverageRoundTrip(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: projectID})

	if _, err := exec(ctx, agent.ToolCall{Tool: "set_coverage", Args: map[string]any{"item": "OWASP-A01", "status": "covered", "note": "tested"}}); err != nil {
		t.Fatal(err)
	}
	out, err := exec(ctx, agent.ToolCall{Tool: "get_coverage", Args: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "OWASP-A01") || !strings.Contains(out, "covered") {
		t.Fatalf("coverage not recorded: %s", out)
	}
}

func TestCreateFindingTool(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), ProjectID: projectID})

	out, err := exec(ctx, agent.ToolCall{Tool: "create_finding", Args: map[string]any{
		"title": "Reflected XSS in search", "severity": "high", "description": "…", "cwe": "CWE-79",
	}})
	if err != nil {
		t.Fatal(err)
	}
	var f model.Finding
	if err := json.Unmarshal([]byte(out), &f); err != nil {
		t.Fatal(err)
	}
	if f.ID == "" || f.Severity != "high" || f.Title != "Reflected XSS in search" {
		t.Fatalf("finding not created as specified: %+v", f)
	}
}

func TestSendRequestScopeGuard(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)
	// Only acme.test is in scope.
	if _, err := db.AddScopeEntry(ctx, projectID, "domain", "acme.test", "allow"); err != nil {
		t.Fatal(err)
	}
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), Replay: replay.New(0), ProjectID: projectID})

	// Out-of-scope target is refused before anything is sent.
	if _, err := exec(ctx, agent.ToolCall{Tool: "send_request", Args: map[string]any{"method": "GET", "url": "https://evil.test/x"}}); err == nil || !strings.Contains(err.Error(), "scope") {
		t.Fatalf("out-of-scope send should be blocked by scope guard, got %v", err)
	}
}

func TestSendRequestSendsAndRecords(t *testing.T) {
	ctx := context.Background()
	db, projectID := seedProject(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(201)
		_, _ = w.Write([]byte("pong"))
	}))
	defer srv.Close()
	// Allow the loopback host the test server runs on.
	if _, err := db.AddScopeEntry(ctx, projectID, "host", "127.0.0.1", "allow"); err != nil {
		t.Fatal(err)
	}
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), Replay: replay.New(0), ProjectID: projectID})

	out, err := exec(ctx, agent.ToolCall{Tool: "send_request", Args: map[string]any{"method": "GET", "url": srv.URL}})
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		ExchangeID   string `json:"exchange_id"`
		Status       int    `json:"status"`
		ResponseBody string `json:"response_body"`
	}
	if err := json.Unmarshal([]byte(unwrapForTest(out)), &res); err != nil {
		t.Fatal(err)
	}
	if res.Status != 201 || res.ResponseBody != "pong" {
		t.Fatalf("unexpected response summary: %s", out)
	}
	// The exchange was persisted with the recorded response.
	ex, err := db.GetExchange(ctx, res.ExchangeID)
	if err != nil {
		t.Fatal(err)
	}
	if ex.Status == nil || *ex.Status != 201 || ex.Origin != "replay" {
		t.Fatalf("exchange not recorded correctly: %+v", ex)
	}
}

func TestProjectScopedToolsNeedProject(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db)}) // no project

	for _, tool := range []string{"list_exchanges", "get_coverage", "set_coverage", "send_request"} {
		args := map[string]any{"item": "x", "status": "covered", "title": "t", "severity": "low", "method": "GET", "url": "https://acme.test"}
		if _, err := exec(ctx, agent.ToolCall{Tool: tool, Args: args}); err == nil || !strings.Contains(err.Error(), "project") {
			t.Fatalf("%s without a project should error about the project, got %v", tool, err)
		}
	}
}
