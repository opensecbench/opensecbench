package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
)

// The agent-playbook editor (ADR-0019): a saved playbook can be created, fetched with its gates, edited in
// place keeping its id, and built-ins remain immutable.
func TestAgentPlaybookEditRoundTrip(t *testing.T) {
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close() })

	// Create with a gate step.
	create := `{"name":"My flow","goal":"g","steps":[
		{"key":"scan","profile":"code-analysis","instruction":"look","depends_on":[]},
		{"key":"approve","gate":true,"depends_on":["scan"]},
		{"key":"report","profile":"report-writer","instruction":"write","depends_on":["approve"]}
	]}`
	var created struct {
		ID string `json:"id"`
	}
	if code := postJSON(t, srv.URL+"/v1/analyst/playbooks", create, &created); code != http.StatusCreated {
		t.Fatalf("create = %d", code)
	}

	// GET returns the gate (the whole point of the read-view fix).
	var got struct {
		ID    string `json:"id"`
		Steps []struct {
			Key  string `json:"key"`
			Gate bool   `json:"gate"`
		} `json:"steps"`
		Builtin bool `json:"builtin"`
	}
	postGet(t, srv.URL+"/v1/analyst/playbooks/"+created.ID, &got)
	if len(got.Steps) != 3 || !got.Steps[1].Gate {
		t.Fatalf("gate not round-tripped on read: %+v", got.Steps)
	}

	// Edit in place — same id, changed name and steps.
	edit := `{"name":"Renamed","goal":"g2","steps":[{"key":"scan","profile":"code-analysis","instruction":"look harder","depends_on":[]}]}`
	var updated struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if code := putJSON(t, srv.URL+"/v1/analyst/playbooks/"+created.ID, edit, &updated); code != http.StatusOK {
		t.Fatalf("update = %d", code)
	}
	if updated.ID != created.ID || updated.Name != "Renamed" {
		t.Fatalf("update should keep id and change name: %+v", updated)
	}

	// Invalid edit (forward dependency) is rejected.
	bad := `{"name":"x","steps":[{"key":"a","profile":"code-analysis","instruction":"i","depends_on":["z"]}]}`
	if code := putJSON(t, srv.URL+"/v1/analyst/playbooks/"+created.ID, bad, nil); code != http.StatusBadRequest {
		t.Fatalf("invalid dependency should be 400, got %d", code)
	}

	// Built-ins are immutable.
	if code := putJSON(t, srv.URL+"/v1/analyst/playbooks/recon", `{"name":"hack","steps":[{"key":"a","profile":"code-analysis","instruction":"i","depends_on":[]}]}`, nil); code != http.StatusBadRequest {
		t.Fatalf("editing a built-in should be 400, got %d", code)
	}

	// Unknown id → 404.
	if code := putJSON(t, srv.URL+"/v1/analyst/playbooks/nope-xyz", `{"name":"x","steps":[{"key":"a","profile":"code-analysis","instruction":"i","depends_on":[]}]}`, nil); code != http.StatusNotFound {
		t.Fatalf("unknown id should be 404, got %d", code)
	}
}
