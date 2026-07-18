package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestSearchAcrossEntities(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	proj, _ := db.CreateProject(ctx, NewProject{Name: "Acme Payments Assessment"})
	app, _ := db.CreateApplication(ctx, proj.ID, "payments-api")
	if _, err := db.CreateAsset(ctx, NewAsset{ApplicationID: app.ID, Type: model.AssetSourceRepo, Location: "/work/acme/payments"}); err != nil {
		t.Fatal(err)
	}

	byName, err := db.Search(ctx, "payments", 25)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]int{}
	for _, r := range byName {
		kinds[r.Kind]++
	}
	if kinds["project"] == 0 || kinds["application"] == 0 || kinds["asset"] == 0 {
		t.Fatalf("expected project+application+asset hits, got %+v", kinds)
	}

	// Empty query returns nothing.
	empty, err := db.Search(ctx, "   ", 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty) != 0 {
		t.Fatalf("empty query returned %d results", len(empty))
	}
}
