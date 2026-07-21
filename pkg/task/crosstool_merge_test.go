package task

import (
	"context"
	"strings"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/capability"
	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// vulnIDs extracts advisory ids from the rule id, the aliases attribute, AND the finding text — so a
// grype GHSA finding that names the CVE in its message can match an osv/govulncheck CVE finding.
func TestVulnIDsScansFindingText(t *testing.T) {
	o := model.Observation{
		RuleID: "GHSA-abcd-efgh-ijkl",
		Detail: "Denial of service in x/net. Fixed in v0.5.0; see CVE-2022-41723.",
	}
	ids := vulnIDs(&o)
	got := map[string]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if !got["GHSA-abcd-efgh-ijkl"] {
		t.Errorf("missing GHSA from rule id; got %v", ids)
	}
	if !got["CVE-2022-41723"] {
		t.Errorf("missing CVE extracted from detail text; got %v", ids)
	}
}

// The same CVE reported by a second tool merges into the first observation: no duplicate, both tools
// recorded, the higher severity kept, and a reachability verdict adopted.
func TestCrossToolMergeCollapsesSameVuln(t *testing.T) {
	db, blobs := openStore(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "P"})

	// Tool 1 (grype): a high-severity CVE, no reachability verdict of its own.
	a, err := db.CreateObservation(ctx, model.Observation{
		ProjectID: &proj.ID, Origin: model.OriginTool, ReviewState: model.ReviewUnreviewed,
		Title: "vuln in golang.org/x/net", Detail: "grype report", Severity: "high",
		RuleID: "CVE-2022-41723", Location: "go.mod:5",
		Attributes: map[string]string{"tool": "grype", "package": "golang.org/x/net"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.RecordObservationVulns(ctx, proj.ID, a.ID, []string{"CVE-2022-41723"}); err != nil {
		t.Fatal(err)
	}

	eng := NewEngine(store.NewCombinedManager(db), cas.Fixed(blobs), capability.BuiltIns(), fakeRunner{code: 0})
	defer eng.Close()

	// Tool 2 (govulncheck): same CVE, critical, proven reachable. Different detail → different fingerprint,
	// so without cross-tool merge it would create a second observation.
	b := model.Observation{
		Severity: "critical", RuleID: "CVE-2022-41723", Location: "golang.org/x/net",
		Attributes: map[string]string{"tool": "govulncheck", "reachable": "true", "aliases": "GO-2022-0969"},
	}
	ids := vulnIDs(&b)
	existingID, dup := db.ObservationForVuln(ctx, proj.ID, ids)
	if !dup || existingID != a.ID {
		t.Fatalf("second tool's CVE should resolve to the first observation; got id=%q dup=%v", existingID, dup)
	}
	eng.mergeVulnObservation(ctx, proj.ID, existingID, &b, ids)

	// Exactly one observation survives.
	all, _ := db.ListObservationsByProject(ctx, proj.ID)
	if len(all) != 1 {
		t.Fatalf("expected 1 merged observation, got %d", len(all))
	}
	merged := all[0]
	if merged.Severity != "critical" {
		t.Fatalf("merged severity = %q, want critical (upgraded)", merged.Severity)
	}
	if merged.Attributes["reachable"] != "true" {
		t.Fatalf("merged should adopt reachable=true; attrs=%v", merged.Attributes)
	}
	tools := merged.Attributes["tools"]
	if !strings.Contains(tools, "grype") || !strings.Contains(tools, "govulncheck") {
		t.Fatalf("merged tools = %q, want both grype and govulncheck", tools)
	}
	// The CVE still resolves to the merged observation, so any later tool reporting it merges too.
	if _, dup := db.ObservationForVuln(ctx, proj.ID, []string{"CVE-2022-41723"}); !dup {
		t.Fatal("the CVE should still resolve to the merged observation")
	}
}
