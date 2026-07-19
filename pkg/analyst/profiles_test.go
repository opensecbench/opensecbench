package analyst

import (
	"context"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/store"
)

func catalogNames() map[string]bool {
	names := map[string]bool{}
	for _, t := range Tools() {
		names[t.Name] = true
	}
	return names
}

func TestProfileToolNamesExistInCatalog(t *testing.T) {
	catalog := catalogNames()
	for _, p := range Profiles() {
		for _, name := range p.Tools {
			if !catalog[name] {
				t.Errorf("profile %q lists tool %q which is not in the catalog", p.ID, name)
			}
		}
	}
}

func TestProfileToolSetLeastPrivilege(t *testing.T) {
	has := func(p Profile, name string) bool {
		for _, tl := range p.ToolSet() {
			if tl.Name == name {
				return true
			}
		}
		return false
	}

	// Generalist: empty allow-list → the full catalog.
	gen := ProfileByID("generalist")
	if len(gen.ToolSet()) != len(Tools()) {
		t.Fatalf("generalist should expose the full catalog: %d vs %d", len(gen.ToolSet()), len(Tools()))
	}

	// Report Writer can write findings but physically cannot send traffic, scan, or execute code.
	rw := ProfileByID("report-writer")
	if !has(rw, "create_finding") || !has(rw, "read_file") {
		t.Fatal("report-writer should have create_finding + reads")
	}
	for _, denied := range []string{"send_request", "run_capability", "run_code", "run_playbook"} {
		if has(rw, denied) {
			t.Fatalf("report-writer must not have %q", denied)
		}
	}

	// Code Analysis reads + runs code but does not send live traffic.
	ca := ProfileByID("code-analysis")
	if !has(ca, "run_code") || has(ca, "send_request") {
		t.Fatal("code-analysis should have run_code but not send_request")
	}

	// Every specialist can read the corpus.
	for _, id := range []string{"code-analysis", "vuln-validator", "pentester", "report-writer"} {
		p := ProfileByID(id)
		if !has(p, "read_file") || !has(p, "get_finding") {
			t.Fatalf("profile %q should be able to read the corpus", id)
		}
	}
}

func TestProfileSystemPrompt(t *testing.T) {
	p := ProfileByID("pentester")
	sp := p.SystemPrompt()
	if !strings.Contains(sp, "penetration tester") {
		t.Fatal("system prompt missing the profile persona")
	}
	// The safety invariants are always appended and non-overridable.
	if !strings.Contains(sp, "Never invent") || !strings.Contains(sp, "raw host shell") {
		t.Fatalf("system prompt missing shared invariants: %q", sp)
	}
}

func TestProfileByIDFallsBackToGeneralist(t *testing.T) {
	if ProfileByID("nope").ID != "generalist" {
		t.Fatal("unknown profile id should fall back to generalist")
	}
	if ProfileByID("").ID != "generalist" {
		t.Fatal("empty profile id should fall back to generalist")
	}
}

func TestThreadAgentTypePersists(t *testing.T) {
	ctx := context.Background()
	db := migratedStore(t)

	th, err := db.CreateThread(ctx, store.NewThread{Title: "recon", AgentType: "pentester"})
	if err != nil {
		t.Fatal(err)
	}
	if th.AgentType != "pentester" {
		t.Fatalf("create: agent_type = %q", th.AgentType)
	}
	got, err := db.GetThread(ctx, th.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.AgentType != "pentester" {
		t.Fatalf("reload: agent_type = %q", got.AgentType)
	}
	// Default when unspecified.
	def, _ := db.CreateThread(ctx, store.NewThread{Title: "x"})
	if def.AgentType != "generalist" {
		t.Fatalf("default agent_type = %q, want generalist", def.AgentType)
	}
}
