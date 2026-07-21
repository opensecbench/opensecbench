package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestReachabilityFactsAggregate(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "P"})
	const subj = "obs-1"

	add := func(source, verdict, conf string) {
		if err := db.AddReachabilityFact(ctx, model.ReachabilityFact{
			ProjectID: proj.ID, SubjectType: model.ReachSubjectObservation, SubjectKey: subj,
			Reachable: verdict, Confidence: conf, Source: source, Rationale: source + " says so",
		}); err != nil {
			t.Fatal(err)
		}
	}

	// A heuristic tool verdict: reachable, medium confidence.
	add("opengrep", model.ReachReachable, model.ReachConfMedium)
	if v, c, _ := db.ResolveReachability(ctx, proj.ID, model.ReachSubjectObservation, subj); v != model.ReachReachable || c != model.ReachConfMedium {
		t.Fatalf("one fact → (%s,%s), want reachable/medium", v, c)
	}

	// An LLM investigation disagrees at higher confidence → it wins.
	add("llm", model.ReachUnreachable, model.ReachConfHigh)
	if v, c, facts := db.ResolveReachability(ctx, proj.ID, model.ReachSubjectObservation, subj); v != model.ReachUnreachable || c != model.ReachConfHigh {
		t.Fatalf("llm high-confidence should win → (%s,%s), want unreachable/high (facts=%d)", v, c, len(facts))
	}

	// A human proves it reachable → proven beats everything.
	add("manual", model.ReachReachable, model.ReachConfProven)
	v, c, facts := db.ResolveReachability(ctx, proj.ID, model.ReachSubjectObservation, subj)
	if v != model.ReachReachable || c != model.ReachConfProven {
		t.Fatalf("proven manual verdict should win → (%s,%s), want reachable/proven", v, c)
	}
	// Three sources, three distinct facts — none overwrote another.
	if len(facts) != 3 {
		t.Fatalf("expected 3 facts (one per source), got %d", len(facts))
	}

	// Re-running a source replaces its own fact, not the others.
	add("opengrep", model.ReachUnknown, model.ReachConfLow)
	if _, _, facts := db.ResolveReachability(ctx, proj.ID, model.ReachSubjectObservation, subj); len(facts) != 3 {
		t.Fatalf("re-adding a source should upsert, not stack; got %d facts", len(facts))
	}
}

// On a confidence tie, "reachable" wins — we never hide a reachable path.
func TestReachabilityTiePrefersReachable(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "P"})
	const subj = "CVE-2022-41723"
	_ = db.AddReachabilityFact(ctx, model.ReachabilityFact{ProjectID: proj.ID, SubjectType: model.ReachSubjectCVE, SubjectKey: subj, Reachable: model.ReachUnreachable, Confidence: model.ReachConfHigh, Source: "toolA"})
	_ = db.AddReachabilityFact(ctx, model.ReachabilityFact{ProjectID: proj.ID, SubjectType: model.ReachSubjectCVE, SubjectKey: subj, Reachable: model.ReachReachable, Confidence: model.ReachConfHigh, Source: "toolB"})
	if v, _, _ := db.ResolveReachability(ctx, proj.ID, model.ReachSubjectCVE, subj); v != model.ReachReachable {
		t.Fatalf("tie should resolve to reachable, got %s", v)
	}
}
