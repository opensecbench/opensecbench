package client

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/api"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
)

func newServer(t *testing.T) *Client {
	t.Helper()
	db := storetest.New(t)
	srv := httptest.NewServer(api.New(api.Deps{Store: store.NewCombinedManager(db)}).Handler())
	t.Cleanup(func() {
		srv.Close()
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
