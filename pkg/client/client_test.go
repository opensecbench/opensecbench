package client

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/api"
	"github.com/opensecbench/opensecbench/pkg/store"
)

func newServer(t *testing.T) *Client {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	ms, err := store.LoadMigrations(migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Apply(ms); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.New(api.Deps{Store: db}).Handler())
	t.Cleanup(func() {
		srv.Close()
		_ = db.Close()
	})
	return New(srv.URL)
}

func TestClientProjectFlow(t *testing.T) {
	c := newServer(t)
	ctx := context.Background()

	if h, err := c.Health(ctx); err != nil || h["status"] != "ok" {
		t.Fatalf("health = %v, err = %v", h, err)
	}

	p, err := c.CreateProject(ctx, CreateProjectRequest{Name: "CLI test"})
	if err != nil {
		t.Fatal(err)
	}
	if p.ID == "" || p.Name != "CLI test" {
		t.Fatalf("unexpected project: %+v", p)
	}

	got, err := c.GetProject(ctx, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != p.ID {
		t.Fatalf("get returned %s, want %s", got.ID, p.ID)
	}

	list, err := c.ListProjects(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("listed %d, want 1", len(list))
	}

	if err := c.DeleteProject(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.GetProject(ctx, p.ID); err == nil {
		t.Fatal("expected error getting deleted project")
	}
}
