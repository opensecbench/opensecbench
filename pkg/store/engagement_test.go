package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestEngagementRoundTrip(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "eng"})

	// No record yet.
	if _, err := db.GetEngagement(ctx, proj.ID); err != ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}

	in := model.Engagement{
		ProjectID:   proj.ID,
		Kinds:       []string{"web", "api"},
		Objective:   "Assess storefront before launch",
		Environment: model.EnvStaging,
		DataClass:   model.DataRestricted,
		Authorized:  true,
		Authorizer:  "j.rivera@acme.com",
		AuthTo:      "2026-08-08",
		Techniques:  map[string]bool{"intrusive": true, "dos": false},
		Contacts: []model.EngagementContact{
			{Role: model.ContactBreakGlass, Name: "Ops on-call", Email: "oncall@acme.com"},
			{Name: "", Email: ""}, // blank contact should be dropped
		},
		TestAccounts: []model.EngagementTestAccount{
			{Role: "admin", Username: "admin@acme.com", SecretRef: "acme-admin-pw"},
		},
	}
	if _, err := db.SetEngagement(ctx, in); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetEngagement(ctx, proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Kinds) != 2 || got.Kinds[0] != "web" || got.DataClass != model.DataRestricted || !got.Authorized {
		t.Fatalf("scalar fields wrong: %+v", got)
	}
	if !got.Techniques["intrusive"] || got.Techniques["dos"] {
		t.Fatalf("techniques wrong: %v", got.Techniques)
	}
	if len(got.Contacts) != 1 || got.Contacts[0].Role != model.ContactBreakGlass {
		t.Fatalf("contacts wrong (blank should drop): %+v", got.Contacts)
	}
	if len(got.TestAccounts) != 1 || got.TestAccounts[0].SecretRef != "acme-admin-pw" {
		t.Fatalf("test accounts wrong: %+v", got.TestAccounts)
	}
	created := got.CreatedAt

	// Update replaces children and preserves created_at.
	in.Objective = "Revised objective"
	in.Contacts = nil
	if _, err := db.SetEngagement(ctx, in); err != nil {
		t.Fatal(err)
	}
	got2, _ := db.GetEngagement(ctx, proj.ID)
	if got2.Objective != "Revised objective" || len(got2.Contacts) != 0 {
		t.Fatalf("update wrong: %+v", got2)
	}
	if !got2.CreatedAt.Equal(created) {
		t.Errorf("created_at not preserved: %v vs %v", got2.CreatedAt, created)
	}
}

func TestScopeDispositionPersists(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	proj, _ := db.CreateProject(ctx, NewProject{Name: "eng"})

	if _, err := db.AddScopeEntry(ctx, proj.ID, "domain", "acme.com", "allow"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.AddScopeEntry(ctx, proj.ID, "host", "payments.acme.com", "deny"); err != nil {
		t.Fatal(err)
	}
	// Unknown disposition defaults to allow.
	e, err := db.AddScopeEntry(ctx, proj.ID, "host", "api.acme.com", "")
	if err != nil {
		t.Fatal(err)
	}
	if e.Disposition != model.ScopeAllow {
		t.Fatalf("empty disposition should default allow, got %q", e.Disposition)
	}
	entries, _ := db.ListScopeEntries(ctx, proj.ID)
	var deny int
	for _, en := range entries {
		if en.Disposition == model.ScopeDeny {
			deny++
		}
	}
	if deny != 1 {
		t.Fatalf("expected 1 deny entry, got %d of %d", deny, len(entries))
	}
}
