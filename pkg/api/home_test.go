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
			RunningTasks int   `json:"running_tasks"`
			Threads      []any `json:"threads"`
		} `json:"active"`
		Projects []any `json:"projects"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&home); err != nil {
		t.Fatal(err)
	}
	// A fresh store: the cockpit responds with present (empty, not null) sections.
	if home.Approvals == nil || home.Active.Threads == nil {
		t.Fatal("cockpit sections should be present (empty arrays), not null")
	}
}
