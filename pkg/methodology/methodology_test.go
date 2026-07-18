package methodology

import (
	"strings"
	"testing"
)

func TestBuiltInsWellFormed(t *testing.T) {
	r := BuiltIns()
	all := r.All()
	if len(all) < 3 {
		t.Fatalf("expected >=3 packs, got %d", len(all))
	}

	ids := map[string]bool{}
	for _, m := range all {
		if m.ID == "" || m.Title == "" || m.Version == "" || len(m.Items) == 0 {
			t.Fatalf("malformed pack: %+v", m)
		}
		for _, it := range m.Items {
			if it.ID == "" || it.Title == "" || it.Procedure == "" {
				t.Fatalf("malformed item in %s: %+v", m.ID, it)
			}
			// Item IDs are pack-scoped and globally unique.
			if !strings.HasPrefix(it.ID, m.ID+"/") {
				t.Fatalf("item %q not scoped to pack %q", it.ID, m.ID)
			}
			if ids[it.ID] {
				t.Fatalf("duplicate item id %q", it.ID)
			}
			ids[it.ID] = true
		}
	}
}

func TestItemLookup(t *testing.T) {
	r := BuiltIns()
	it, pack, ok := r.Item("web-app/idor-nope")
	if ok {
		t.Fatal("unexpected hit for missing item")
	}
	it, pack, ok = r.Item("oidc-oauth/pkce")
	if !ok || it.Title == "" || pack.ID != "oidc-oauth" {
		t.Fatalf("lookup failed: %+v %+v %v", it, pack, ok)
	}
}

func TestBuildCoverage(t *testing.T) {
	reg := BuiltIns()
	adopted := []string{"oidc-oauth"} // 4 items
	states := map[string]State{
		"oidc-oauth/pkce":           {Status: "covered"},
		"oidc-oauth/state-csrf":     {Status: "covered"},
		"oidc-oauth/token-handling": {Status: "not_applicable", Note: "no JWTs"},
		// redirect-uri left unset -> not_started
	}
	v := BuildCoverage(reg, adopted, states)
	if len(v.Packs) != 1 || len(v.Packs[0].Items) != 4 {
		t.Fatalf("expected 1 pack with 4 items, got %+v", v.Packs)
	}
	s := v.Summary
	if s.Total != 4 || s.Covered != 2 || s.NotApplicable != 1 || s.NotStarted != 1 {
		t.Fatalf("summary counts wrong: %+v", s)
	}
	// covered / (total - n/a) = 2 / 3 = 66%
	if s.CoveredPct != 66 {
		t.Fatalf("covered_pct = %d, want 66", s.CoveredPct)
	}
}

func TestBuildCoverageEmptyDenominator(t *testing.T) {
	reg := BuiltIns()
	states := map[string]State{}
	for _, it := range reg.packs["rest-api"].Items {
		states[it.ID] = State{Status: "not_applicable"}
	}
	v := BuildCoverage(reg, []string{"rest-api"}, states)
	if v.Summary.CoveredPct != 0 {
		t.Fatalf("all-n/a should be 0%%, got %d", v.Summary.CoveredPct)
	}
}
