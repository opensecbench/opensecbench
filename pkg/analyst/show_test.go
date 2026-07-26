package analyst

import (
	"testing"

	"github.com/opensecbench/opensecbench/pkg/agent"
)

func TestRunShowPublishesUICommand(t *testing.T) {
	var gotProj string
	var got UICommand
	svc := &Service{}
	svc.SetUIPublisher(func(p string, c UICommand) { gotProj = p; got = c })

	out, err := svc.runShow("proj1", agent.ToolCall{Tool: "show", Args: map[string]any{"kind": "finding", "id": "f1"}})
	if err != nil {
		t.Fatalf("runShow: %v", err)
	}
	if out == "" {
		t.Fatal("expected a non-empty tool result")
	}
	if gotProj != "proj1" || got.Action != "show" || got.Kind != "finding" || got.ID != "f1" {
		t.Fatalf("published = %+v proj=%q, want show/finding/f1 on proj1", got, gotProj)
	}
}

func TestRunShowCodeCarriesLocation(t *testing.T) {
	var got UICommand
	svc := &Service{}
	svc.SetUIPublisher(func(_ string, c UICommand) { got = c })
	if _, err := svc.runShow("p", agent.ToolCall{Tool: "show", Args: map[string]any{
		"kind": "code", "id": "asset1", "location": "cmd/server/main.go:42",
	}}); err != nil {
		t.Fatalf("runShow: %v", err)
	}
	if got.Kind != "code" || got.ID != "asset1" || got.Location != "cmd/server/main.go:42" {
		t.Fatalf("published = %+v, want code/asset1 at main.go:42", got)
	}
}

// A missing publisher must not panic — headless runs have none; show just reports success.
func TestRunShowNoPublisher(t *testing.T) {
	svc := &Service{}
	if _, err := svc.runShow("p", agent.ToolCall{Tool: "show", Args: map[string]any{"kind": "surface", "id": "findings"}}); err != nil {
		t.Fatalf("runShow with no publisher: %v", err)
	}
}

func TestRunShowValidatesArgs(t *testing.T) {
	svc := &Service{}
	svc.SetUIPublisher(func(string, UICommand) { t.Fatal("must not publish an invalid command") })
	cases := map[string]map[string]any{
		"missing kind":          {"id": "x"},
		"finding without id":    {"kind": "finding"},
		"code without id":       {"kind": "code", "location": "a.go:1"},
		"code without location": {"kind": "code", "id": "asset1"},
		"surface without id":    {"kind": "surface"},
	}
	for name, args := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.runShow("p", agent.ToolCall{Tool: "show", Args: args}); err == nil {
				t.Fatal("expected a validation error")
			}
		})
	}
}

func TestShowToolIsInCatalogAndAutoApproved(t *testing.T) {
	var found bool
	for _, tl := range Tools() {
		if tl.Name == "show" {
			found = true
		}
	}
	if !found {
		t.Fatal("show tool missing from catalog")
	}
	if DefaultPolicy().NeedsApproval("show", "generalist") {
		t.Fatal("show must be auto-approved (read-only navigation), not gated")
	}
}
