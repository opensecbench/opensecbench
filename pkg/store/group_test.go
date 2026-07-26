package store

import (
	"context"
	"testing"
)

func TestGroupsCRUDAndOrgScope(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	org, err := db.CreateOrganization(ctx, "Acme")
	if err != nil {
		t.Fatal(err)
	}
	g, err := db.CreateGroup(ctx, org.ID, "Platform")
	if err != nil {
		t.Fatalf("CreateGroup: %v", err)
	}
	if g.OrganizationID != org.ID || g.Name != "Platform" || g.ID == "" {
		t.Fatalf("bad group %+v", g)
	}
	// A group under a different org must not appear when scoping to org.
	org2, _ := db.CreateOrganization(ctx, "Beta")
	if _, err := db.CreateGroup(ctx, org2.ID, "Other"); err != nil {
		t.Fatal(err)
	}
	scoped, err := db.ListGroups(ctx, org.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != 1 || scoped[0].Name != "Platform" {
		t.Fatalf("org-scoped list = %+v, want just Platform", scoped)
	}
	if all, _ := db.ListGroups(ctx, ""); len(all) != 2 {
		t.Fatalf("unscoped list = %d, want 2", len(all))
	}
	// A group needs a name and an org.
	if _, err := db.CreateGroup(ctx, org.ID, ""); err == nil {
		t.Fatal("expected error for empty name")
	}
	if _, err := db.CreateGroup(ctx, "", "x"); err == nil {
		t.Fatal("expected error for missing org")
	}
}
