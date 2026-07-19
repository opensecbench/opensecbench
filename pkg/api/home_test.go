package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestHomeCockpit(t *testing.T) {
	srv := newTestServer(t)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/v1/home")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var home struct {
		Approvals []any `json:"approvals"`
		Active    struct {
			Tasks   []any `json:"tasks"`
			Threads []any `json:"threads"`
		} `json:"active"`
		Projects  []any `json:"projects"`
		Schedules []any `json:"schedules"`
		Usage     struct {
			AllInput  int   `json:"all_input"`
			TopModels []any `json:"top_models"`
		} `json:"usage"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&home); err != nil {
		t.Fatal(err)
	}
	// A fresh store: the cockpit responds with present (empty, not null) sections.
	if home.Approvals == nil || home.Active.Threads == nil || home.Active.Tasks == nil || home.Schedules == nil {
		t.Fatal("cockpit sections should be present (empty arrays), not null")
	}
	// Usage is present with zeroed totals on a fresh store (no runs recorded).
	if home.Usage.AllInput != 0 {
		t.Fatalf("fresh store should report zero all-time input tokens, got %d", home.Usage.AllInput)
	}
}
