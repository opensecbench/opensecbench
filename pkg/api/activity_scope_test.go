package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/store"
)

// TestActivityFeedScopedToProject proves ?project= restricts the feed to that project's runs — so a
// project's Activity surface doesn't show another project's threads/tasks (and flat-route detail fetches,
// which resolve via the active project's DB, never 404 on a foreign item).
func TestActivityFeedScopedToProject(t *testing.T) {
	srv, db := newAsyncTaskServer(t)
	ctx := t.Context()

	proj1, err := db.CreateProject(ctx, store.NewProject{Name: "P1"})
	if err != nil {
		t.Fatal(err)
	}
	proj2, err := db.CreateProject(ctx, store.NewProject{Name: "P2"})
	if err != nil {
		t.Fatal(err)
	}
	p1, p2 := proj1.ID, proj2.ID
	mine, err := db.CreateThread(ctx, store.NewThread{ProjectID: &p1, Title: "mine", Provider: "anthropic", AgentType: "pentest"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreateThread(ctx, store.NewThread{ProjectID: &p2, Title: "theirs", Provider: "anthropic", AgentType: "pentest"}); err != nil {
		t.Fatal(err)
	}

	get := func(url string) []map[string]any {
		resp, err := http.Get(url)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("GET %s = %d", url, resp.StatusCode)
		}
		var feed []map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&feed); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return feed
	}

	scoped := get(srv.URL + "/v1/activity/feed?project=" + p1)
	for _, it := range scoped {
		if it["kind"] == "thread" && it["title"] == "theirs" {
			t.Fatalf("scoped feed leaked another project's thread: %+v", scoped)
		}
	}
	var sawMine bool
	for _, it := range scoped {
		if it["id"] == mine.ID {
			sawMine = true
		}
	}
	if !sawMine {
		t.Fatalf("scoped feed missing this project's thread; got %+v", scoped)
	}

	// Unscoped still spans projects (both threads present).
	all := get(srv.URL + "/v1/activity/feed")
	titles := map[string]bool{}
	for _, it := range all {
		if s, ok := it["title"].(string); ok {
			titles[s] = true
		}
	}
	if !titles["mine"] || !titles["theirs"] {
		t.Fatalf("unscoped feed should span projects; got %+v", all)
	}
}
