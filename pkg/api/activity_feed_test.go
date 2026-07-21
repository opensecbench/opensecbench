package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
)

// TestActivityFeedMergesKinds proves the unified feed interleaves a scanner task and an agent thread —
// the durable "what ran" surface — so an agent conversation stays browsable alongside tool runs.
func TestActivityFeedMergesKinds(t *testing.T) {
	srv, db := newAsyncTaskServer(t)
	ctx := t.Context()

	// A scanner task (enqueued through the API so it flows the normal path).
	var task model.Task
	if code := postJSON(t, srv.URL+"/v1/tasks", `{"capability_id":"source-inventory","target_dir":"/x","actor":"human"}`, &task); code != http.StatusAccepted {
		t.Fatalf("POST /v1/tasks = %d", code)
	}

	// An agent thread with a message, written directly to the store (the transcript that must survive).
	th, err := db.CreateThread(ctx, store.NewThread{Title: "sqli recon", Provider: "anthropic", AgentType: "pentest"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.AppendMessage(ctx, th.ID, "assistant", "I found a candidate injection point."); err != nil {
		t.Fatal(err)
	}

	resp, err := http.Get(srv.URL + "/v1/activity/feed")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/activity/feed = %d", resp.StatusCode)
	}
	var feed []struct {
		Kind  string `json:"kind"`
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&feed); err != nil {
		t.Fatalf("decode: %v", err)
	}

	kinds := map[string]string{}
	for _, it := range feed {
		kinds[it.Kind] = it.Title
	}
	if _, ok := kinds["task"]; !ok {
		t.Errorf("feed missing a task item; got %+v", feed)
	}
	if kinds["thread"] != "sqli recon" {
		t.Errorf("feed missing the agent thread; got %+v", feed)
	}
}
