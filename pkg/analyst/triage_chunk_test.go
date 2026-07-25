package analyst

import (
	"fmt"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func obsAt(id, file string, line int, rule string) model.Observation {
	return model.Observation{ID: id, Location: fmt.Sprintf("%s:%d", file, line), RuleID: rule, Severity: "medium"}
}

func idsOf(chunks [][]model.Observation) map[string]int {
	seen := map[string]int{}
	for _, c := range chunks {
		for _, o := range c {
			seen[o.ID]++
		}
	}
	return seen
}

func TestChunkObservations_CoverageAndBounds(t *testing.T) {
	var obs []model.Observation
	// 6 files × varying sizes = 60 observations.
	for f := 0; f < 6; f++ {
		for i := 0; i < 10; i++ {
			obs = append(obs, obsAt(fmt.Sprintf("o-%d-%d", f, i), fmt.Sprintf("pkg/f%d.go", f), i+1, "rule.x"))
		}
	}
	chunks := chunkObservations(obs, 8, 25)

	seen := idsOf(chunks)
	if len(seen) != len(obs) {
		t.Fatalf("coverage: got %d distinct ids, want %d", len(seen), len(obs))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("id %s appeared %d times, want exactly 1", id, n)
		}
	}
	for i, c := range chunks {
		if len(c) > 25 {
			t.Fatalf("chunk %d has %d obs, exceeds max 25", i, len(c))
		}
	}
}

func TestChunkObservations_SmallFileStaysTogether(t *testing.T) {
	obs := []model.Observation{
		obsAt("a1", "pkg/a.go", 1, "r"), obsAt("a2", "pkg/a.go", 2, "r"), obsAt("a3", "pkg/a.go", 3, "r"),
		obsAt("b1", "pkg/b.go", 1, "r"), obsAt("b2", "pkg/b.go", 2, "r"),
	}
	chunks := chunkObservations(obs, 8, 25)
	// All 5 fit under max, so they pack into a single chunk; a.go's three must not be scattered.
	if len(chunks) != 1 {
		t.Fatalf("got %d chunks, want 1 (everything fits under max)", len(chunks))
	}
}

func TestChunkObservations_SplitsOversizeFile(t *testing.T) {
	var obs []model.Observation
	for i := 0; i < 30; i++ { // one file, 30 > max 25
		obs = append(obs, obsAt(fmt.Sprintf("x%d", i), "pkg/big.go", i+1, "r"))
	}
	chunks := chunkObservations(obs, 8, 25)
	if len(chunks) < 2 {
		t.Fatalf("30 obs in one file with max 25 should split into >=2 chunks, got %d", len(chunks))
	}
	if got := len(idsOf(chunks)); got != 30 {
		t.Fatalf("coverage after split: got %d, want 30", got)
	}
	for i, c := range chunks {
		if len(c) > 25 {
			t.Fatalf("chunk %d exceeds max: %d", i, len(c))
		}
	}
}

func TestParseTriageDecisions_TolerantOfProse(t *testing.T) {
	reply := "Sure, here are my verdicts:\n```json\n" +
		`[{"id":"a","disposition":"dismiss","rationale":"unreachable"},{"id":"b","disposition":"flag","rationale":"real sqli"}]` +
		"\n```\nHope that helps!"
	got, err := parseTriageDecisions(reply)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(got) != 2 || got[0].ID != "a" || got[0].Disposition != "dismiss" || got[1].Disposition != "flag" {
		t.Fatalf("unexpected decisions: %+v", got)
	}
}
