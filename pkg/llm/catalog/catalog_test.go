package catalog

import "testing"

func TestCatalogLoadsAndIsWellFormed(t *testing.T) {
	models := Models()
	if len(models) == 0 {
		t.Fatal("catalog is empty")
	}
	seen := map[string]bool{}
	for _, m := range models {
		if m.Provider == "" || m.ID == "" || m.Name == "" {
			t.Fatalf("model missing required fields: %+v", m)
		}
		key := m.Provider + "/" + m.ID
		if seen[key] {
			t.Fatalf("duplicate catalog entry %s", key)
		}
		seen[key] = true
	}
}

func TestCatalogLookups(t *testing.T) {
	if len(ByProvider("anthropic")) == 0 {
		t.Fatal("expected anthropic models")
	}
	if _, ok := Get("anthropic", "claude-sonnet-5"); !ok {
		t.Fatal("expected to find claude-sonnet-5")
	}
	if _, ok := Get("anthropic", "does-not-exist"); ok {
		t.Fatal("unexpected hit for a missing model")
	}
}
