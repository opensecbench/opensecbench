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
