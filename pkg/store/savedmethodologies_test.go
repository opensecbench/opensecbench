package store

import (
	"context"
	"errors"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestSavedMethodologyCRUD(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	created, err := db.CreateSavedMethodology(ctx, model.SavedMethodology{
		ID: "graphql-api", Title: "GraphQL API", Data: []byte(`{"id":"graphql-api","title":"GraphQL API"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.CreatedAt.IsZero() || created.UpdatedAt.IsZero() {
		t.Fatal("timestamps not set on create")
	}

	got, err := db.GetSavedMethodology(ctx, "graphql-api")
	if err != nil || got.Title != "GraphQL API" {
		t.Fatalf("get = %+v (%v)", got, err)
	}

	// Update in place keeps the id and created_at.
	updated, err := db.UpdateSavedMethodology(ctx, model.SavedMethodology{
		ID: "graphql-api", Title: "GraphQL API v2", Data: []byte(`{"id":"graphql-api","title":"GraphQL API v2"}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Title != "GraphQL API v2" || !updated.CreatedAt.Equal(created.CreatedAt) {
		t.Fatalf("update did not preserve created_at or apply title: %+v", updated)
	}

	list, err := db.ListSavedMethodologies(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v (%v), want 1", list, err)
	}

	// Updating a nonexistent (e.g. built-in) pack reports ErrNotFound — that's how built-ins stay immutable.
	if _, err := db.UpdateSavedMethodology(ctx, model.SavedMethodology{ID: "web-app", Title: "x", Data: []byte(`{}`)}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("update of nonexistent = %v, want ErrNotFound", err)
	}

	if err := db.DeleteSavedMethodology(ctx, "graphql-api"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetSavedMethodology(ctx, "graphql-api"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("get after delete = %v, want ErrNotFound", err)
	}
	if err := db.DeleteSavedMethodology(ctx, "graphql-api"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("double delete = %v, want ErrNotFound", err)
	}
}
