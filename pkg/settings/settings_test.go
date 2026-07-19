package settings

import "testing"

func TestNamespace(t *testing.T) {
	secs := Namespace("acme.scanner", []Section{
		{
			ID:    "scanner",
			Title: "Scanner",
			Order: 5,
			Fields: []Field{
				{Key: "scanner.depth", Label: "Depth", Type: TypeNumber, Default: "3"},
				{Key: "scanner.bad", Label: "Bad", Type: "wat"}, // unknown type → dropped
				{Key: "", Label: "No key", Type: TypeBool},       // empty key → dropped
			},
		},
	})

	if len(secs) != 1 {
		t.Fatalf("sections = %d, want 1: %+v", len(secs), secs)
	}
	sec := secs[0]
	if sec.ID != "ext.acme.scanner.scanner" {
		t.Fatalf("section id = %q, want namespaced", sec.ID)
	}
	if sec.Source != "ext:acme.scanner" {
		t.Fatalf("source = %q, want ext:acme.scanner", sec.Source)
	}
	if len(sec.Fields) != 1 {
		t.Fatalf("fields = %d, want 1 (invalid ones dropped): %+v", len(sec.Fields), sec.Fields)
	}
	if sec.Fields[0].Key != "ext.acme.scanner.scanner.depth" {
		t.Fatalf("field key = %q, want namespaced", sec.Fields[0].Key)
	}
	// The namespaced key must be resolvable for write-validation, and never collide with core keys.
	if _, ok := FieldByKey(secs, "ext.acme.scanner.scanner.depth"); !ok {
		t.Fatal("namespaced field should be findable by its key")
	}
	if _, ok := FieldByKey(CoreSections(), "ext.acme.scanner.scanner.depth"); ok {
		t.Fatal("extension key must not resolve against core sections")
	}
}

func TestNamespaceDropsEmptySectionsAndBlankID(t *testing.T) {
	// A section whose fields are all invalid is dropped entirely.
	if got := Namespace("x", []Section{{ID: "s", Fields: []Field{{Key: "s.a", Type: "nope"}}}}); len(got) != 0 {
		t.Fatalf("expected empty result, got %+v", got)
	}
	// A blank extension id contributes nothing (defensive).
	if got := Namespace("", []Section{{ID: "s", Fields: []Field{{Key: "s.a", Type: TypeBool}}}}); got != nil {
		t.Fatalf("blank ext id should yield nil, got %+v", got)
	}
}
