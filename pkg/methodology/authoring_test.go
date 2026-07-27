package methodology

import "testing"

func TestNormalizeDerivesIDsAndDefaults(t *testing.T) {
	m := Methodology{
		Title: "GraphQL API",
		Items: []Item{
			{Title: "Query depth limiting"},
			{Title: "Field authorization", ID: "graphql-api/field-authz"}, // explicit id is kept
		},
		Keywords: []string{" graphql ", "", "gql"},
	}
	Normalize(&m)

	if m.ID != "graphql-api" {
		t.Fatalf("pack id = %q, want graphql-api", m.ID)
	}
	if m.Tech != "custom" || m.Version != "1.0.0" {
		t.Fatalf("defaults not applied: tech=%q version=%q", m.Tech, m.Version)
	}
	if got := m.Items[0].ID; got != "graphql-api/query-depth-limiting" {
		t.Fatalf("derived item id = %q", got)
	}
	if got := m.Items[1].ID; got != "graphql-api/field-authz" {
		t.Fatalf("explicit item id not kept: %q", got)
	}
	if len(m.Keywords) != 2 || m.Keywords[0] != "graphql" {
		t.Fatalf("keywords not cleaned: %v", m.Keywords)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		m       Methodology
		wantErr bool
	}{
		{"ok", Methodology{ID: "p", Title: "P", Items: []Item{{ID: "p/a", Title: "A"}}}, false},
		{"no title", Methodology{ID: "p", Items: []Item{{ID: "p/a", Title: "A"}}}, true},
		{"no items", Methodology{ID: "p", Title: "P"}, true},
		{"item without title", Methodology{ID: "p", Title: "P", Items: []Item{{ID: "p/a"}}}, true},
		{"duplicate item id", Methodology{ID: "p", Title: "P", Items: []Item{{ID: "p/a", Title: "A"}, {ID: "p/a", Title: "B"}}}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Validate(c.m); (err != nil) != c.wantErr {
				t.Fatalf("Validate() err = %v, wantErr = %v", err, c.wantErr)
			}
		})
	}
}

func TestRegistryRegisterAndRemove(t *testing.T) {
	r := BuiltIns()
	before := len(r.All())
	r.Register(Methodology{ID: "custom-pack", Title: "Custom", Items: []Item{{ID: "custom-pack/x", Title: "X"}}})
	if _, ok := r.Get("custom-pack"); !ok {
		t.Fatal("registered pack not found")
	}
	if len(r.All()) != before+1 {
		t.Fatalf("All() = %d, want %d", len(r.All()), before+1)
	}
	if _, _, ok := r.Item("custom-pack/x"); !ok {
		t.Fatal("registered item not resolvable")
	}
	r.Remove("custom-pack")
	if _, ok := r.Get("custom-pack"); ok {
		t.Fatal("removed pack still present")
	}
}
