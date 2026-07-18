package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestKBInheritanceAndReview(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	target, err := db.CreateTarget(ctx, "Acme Platform", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Two engagements against the same durable target.
	p1, _ := db.CreateProject(ctx, NewProject{Name: "2025 assessment", TargetIDs: []string{target.ID}})
	p2, _ := db.CreateProject(ctx, NewProject{Name: "2026 re-assessment", TargetIDs: []string{target.ID}})

	// Human entry (defaults to confirmed) + agent draft (defaults to unreviewed).
	human, err := db.CreateKBEntry(ctx, model.KBEntry{TargetID: target.ID, Kind: model.KBAuth, Title: "Uses SAML SSO via Okta"})
	if err != nil {
		t.Fatal(err)
	}
	if human.ReviewState != model.ReviewConfirmed || human.Origin != model.OriginHuman {
		t.Fatalf("human entry defaults wrong: %+v", human)
	}
	draft, err := db.CreateKBEntry(ctx, model.KBEntry{TargetID: target.ID, Kind: model.KBEndpoint, Title: "GraphQL at /api/graphql", Origin: model.OriginThread})
	if err != nil {
		t.Fatal(err)
	}
	if draft.ReviewState != model.ReviewUnreviewed {
		t.Fatalf("agent draft should be unreviewed: %+v", draft)
	}

	// Both engagements inherit the target's KB.
	for _, pid := range []string{p1.ID, p2.ID} {
		entries, err := db.ListKBByProject(ctx, pid)
		if err != nil {
			t.Fatal(err)
		}
		if len(entries) != 2 {
			t.Fatalf("project %s inherited %d entries, want 2", pid, len(entries))
		}
	}

	// Curate the draft: edit + confirm.
	if err := db.UpdateKBEntry(ctx, draft.ID, "GraphQL API at /api/graphql", "introspection enabled", "graphql,api"); err != nil {
		t.Fatal(err)
	}
	if err := db.ReviewKBEntry(ctx, draft.ID, model.ReviewConfirmed); err != nil {
		t.Fatal(err)
	}
	got, _ := db.GetKBEntry(ctx, draft.ID)
	if got.ReviewState != model.ReviewConfirmed || got.Body != "introspection enabled" || got.Tags != "graphql,api" {
		t.Fatalf("curation not applied: %+v", got)
	}

	// Invalid review state rejected.
	if err := db.ReviewKBEntry(ctx, draft.ID, "bogus"); err == nil {
		t.Fatal("expected invalid review state error")
	}
}
