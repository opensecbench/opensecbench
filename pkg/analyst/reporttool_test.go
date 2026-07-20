package analyst

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// generate_report compiles the project's findings into a stored, downloadable deliverable — a report record
// plus a CAS artifact — and returns their ids. (An empty-findings project still produces a valid report.)
func TestGenerateReport(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	blobs, _ := cas.Open(t.TempDir())
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "Acme"})
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), Blobs: blobs, ProjectID: proj.ID})

	out, err := exec(ctx, agent.ToolCall{Tool: "generate_report", Args: map[string]any{"template": "technical", "format": "md"}})
	if err != nil {
		t.Fatalf("generate_report: %v", err)
	}
	var res struct {
		ReportID   string `json:"report_id"`
		ArtifactID string `json:"artifact_id"`
		Template   string `json:"template"`
		Format     string `json:"format"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatalf("bad json: %v (%s)", err, out)
	}
	if res.ReportID == "" || res.ArtifactID == "" {
		t.Fatalf("expected report + artifact ids: %s", out)
	}
	if res.Template != "technical" || res.Format != "md" {
		t.Fatalf("wrong template/format: %s", out)
	}
	// The report is persisted and points at a real, retrievable artifact.
	reports, err := db.ListReportsByProject(ctx, proj.ID)
	if err != nil || len(reports) != 1 {
		t.Fatalf("report not persisted: %v (%d)", err, len(reports))
	}
	art, err := db.GetArtifact(ctx, res.ArtifactID)
	if err != nil {
		t.Fatalf("artifact not found: %v", err)
	}
	if art.Size == 0 {
		t.Fatal("rendered report is empty")
	}
}

func TestGenerateReportRejectsUnknownTemplate(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	blobs, _ := cas.Open(t.TempDir())
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "Acme"})
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), Blobs: blobs, ProjectID: proj.ID})

	if _, err := exec(ctx, agent.ToolCall{Tool: "generate_report", Args: map[string]any{"template": "nope"}}); err == nil {
		t.Fatal("expected an error for an unknown template")
	}
}
