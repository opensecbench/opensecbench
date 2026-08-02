package model

import (
	"strings"
	"testing"
)

func TestScaleOrderingAndAllows(t *testing.T) {
	// Deliberately unsorted, with a custom tier inserted between internal and private.
	sc := NewScale([]ClassificationLevel{
		{ID: "private", Rank: 20},
		{ID: "open_source", Rank: 0},
		{ID: "internal", Rank: 10},
		{ID: "confidential", Rank: 15},
	})

	if sc.Min() != "open_source" || sc.Max() != "private" {
		t.Fatalf("min/max = %q/%q, want open_source/private", sc.Min(), sc.Max())
	}
	var ids []string
	for _, l := range sc.Levels() {
		ids = append(ids, l.ID)
	}
	if got := strings.Join(ids, ","); got != "open_source,internal,confidential,private" {
		t.Fatalf("order = %q", got)
	}

	// A clearance covers its own tier and every less-sensitive one, but not more-sensitive.
	if !sc.Allows("confidential", "internal") {
		t.Fatal("confidential clearance should allow internal content")
	}
	if sc.Allows("internal", "confidential") {
		t.Fatal("internal clearance must not allow confidential content")
	}
	if !sc.Allows("private", "confidential") {
		t.Fatal("private clearance should allow confidential content")
	}

	// Fail-safe: unknown content sensitivity blocks; unknown clearance permits only the least tier.
	if sc.Allows("private", "unknown") {
		t.Fatal("unknown content sensitivity must be blocked")
	}
	if !sc.Allows("unknown", "open_source") {
		t.Fatal("unknown clearance should still allow the least-sensitive tier")
	}
	if sc.Allows("unknown", "internal") {
		t.Fatal("unknown clearance must not allow anything above the least tier")
	}

	// MinClearance: the lower-rank wins; an empty override inherits the base.
	if got := sc.MinClearance("private", "internal"); got != "internal" {
		t.Fatalf("MinClearance(private,internal) = %q, want internal", got)
	}
	if got := sc.MinClearance("internal", ""); got != "internal" {
		t.Fatalf("MinClearance(internal,\"\") = %q, want internal", got)
	}
}
