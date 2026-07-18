package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestProviderCRUD(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	a, err := db.CreateProvider(ctx, model.Provider{Name: "cloud", Type: "anthropic", Model: "claude-sonnet-5", KeySealed: "sealed-blob"})
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == "" || a.CreatedAt.IsZero() {
		t.Fatalf("create did not populate id/timestamp: %+v", a)
	}
	if _, err := db.CreateProvider(ctx, model.Provider{Name: "local", Type: "ollama", BaseURL: "http://127.0.0.1:11434/v1", Model: "llama3"}); err != nil {
		t.Fatal(err)
	}

	list, err := db.ListProviders(ctx)
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %v (%v), want 2", list, err)
	}

	got, err := db.GetProvider(ctx, a.ID)
	if err != nil || got.KeySealed != "sealed-blob" || got.Type != "anthropic" {
		t.Fatalf("get = %+v (%v)", got, err)
	}

	if err := db.DeleteProvider(ctx, a.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetProvider(ctx, a.ID); err != ErrNotFound {
		t.Fatalf("get after delete = %v, want ErrNotFound", err)
	}

	// name/type are required.
	if _, err := db.CreateProvider(ctx, model.Provider{Type: "anthropic"}); err == nil {
		t.Fatal("expected error for missing name")
	}
}
