package analyst

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/agent"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/llm"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// seedContext creates a project + an ingested context item whose bytes live in a temp CAS.
func seedContext(t *testing.T, content string) (db *store.DB, blobs *cas.Store, projectID, itemID string) {
	t.Helper()
	db = migratedStore(t)
	ctx := context.Background()
	var err error
	blobs, err = cas.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})
	digest, err := blobs.Put(strings.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	art, err := db.CreateArtifact(ctx, model.Artifact{SHA256: digest, MediaType: "text/plain", Size: int64(len(content)), Name: "design.md", Kind: "input"})
	if err != nil {
		t.Fatal(err)
	}
	ci, err := db.CreateContextItem(ctx, model.ContextItem{ProjectID: proj.ID, Type: model.ContextDocument, Name: "design.md", ArtifactID: art.ID})
	if err != nil {
		t.Fatal(err)
	}
	return db, blobs, proj.ID, ci.ID
}

func TestListAndReadContext(t *testing.T) {
	ctx := context.Background()
	const body = "Design doc: auth uses JWT; the password-reset flow skips rate limiting."
	db, blobs, projectID, itemID := seedContext(t, body)
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), Blobs: blobs, ProjectID: projectID})

	out, err := exec(ctx, agent.ToolCall{Tool: "list_context", Args: map[string]any{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "design.md") || !strings.Contains(out, itemID) {
		t.Fatalf("list_context = %s", out)
	}

	out, err = exec(ctx, agent.ToolCall{Tool: "read_context", Args: map[string]any{"id": itemID}})
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(out), &res); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Content, "skips rate limiting") {
		t.Fatalf("read_context content = %q", res.Content)
	}
}

func TestReadContextCrossProjectRefused(t *testing.T) {
	ctx := context.Background()
	db, blobs, _, itemID := seedContext(t, "secret")
	other, _ := db.CreateProject(ctx, store.NewProject{Name: "Other"})
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), Blobs: blobs, ProjectID: other.ID})

	if _, err := exec(ctx, agent.ToolCall{Tool: "read_context", Args: map[string]any{"id": itemID}}); err == nil || !strings.Contains(err.Error(), "different project") {
		t.Fatalf("cross-project read_context should be refused, got %v", err)
	}
}

func TestReadContextBinaryReturnsMetadata(t *testing.T) {
	ctx := context.Background()
	db, blobs, projectID, itemID := seedContext(t, "PDF\x00\x01binary\x00blob")
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db), Blobs: blobs, ProjectID: projectID})

	out, err := exec(ctx, agent.ToolCall{Tool: "read_context", Args: map[string]any{"id": itemID}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "\"content\"") || !strings.Contains(out, "binary document") {
		t.Fatalf("binary context should return a note, not content: %s", out)
	}
}

func TestReadContextEgressBlocked(t *testing.T) {
	ctx := context.Background()
	db, blobs, projectID, itemID := seedContext(t, "client email thread")

	svc := &Service{mgr: store.NewCombinedManager(db), blobs: blobs, egressStrict: true}
	// Strict egress + external provider: ingested corpus content does not leave to the external model.
	if _, err := svc.executeFor(projectID, &llm.AnthropicProvider{})(ctx, agent.ToolCall{Tool: "read_context", Args: map[string]any{"id": itemID}}); err == nil || !strings.Contains(err.Error(), "egress") {
		t.Fatalf("read_context should be egress-blocked on an external provider, got %v", err)
	}
	// A local provider reads it fine.
	if _, err := svc.executeFor(projectID, &llm.MockProvider{})(ctx, agent.ToolCall{Tool: "read_context", Args: map[string]any{"id": itemID}}); err != nil {
		t.Fatalf("local provider read_context should not be blocked: %v", err)
	}
}

func TestGetKBEntry(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)
	tgt, err := db.CreateTarget(ctx, "acme-web", "the app", nil)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := db.CreateKBEntry(ctx, model.KBEntry{TargetID: tgt.ID, Kind: "auth", Title: "Auth model", Body: "JWT in an httpOnly cookie; 30-min expiry."})
	if err != nil {
		t.Fatal(err)
	}
	exec := Executor(ExecDeps{Mgr: store.NewCombinedManager(db)})

	out, err := exec(ctx, agent.ToolCall{Tool: "get_kb_entry", Args: map[string]any{"id": entry.ID}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "httpOnly cookie") {
		t.Fatalf("get_kb_entry should return the body, got %s", out)
	}
}
