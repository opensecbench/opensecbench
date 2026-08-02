package api

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/cas"
	"github.com/opensecbench/opensecbench/pkg/model"
	"github.com/opensecbench/opensecbench/pkg/store"
	"github.com/opensecbench/opensecbench/pkg/store/storetest"
)

// /v1/activity reports in-flight tasks and agent plans across the workbench.
func TestActivityEndpoint(t *testing.T) {
	db := storetest.New(t)
	blobs, _ := cas.Open(filepath.Join(t.TempDir(), "cas"))
	srv := httptest.NewServer(New(Deps{Store: store.NewCombinedManager(db), CAS: blobs}).Handler())
	t.Cleanup(func() { srv.Close() })
	ctx := context.Background()

	proj, _ := db.CreateProject(ctx, store.NewProject{Name: "p"})
	// A pending task (queued, no worker to run it here) and a running plan.
	if _, err := db.CreateTask(ctx, store.NewTask{CapabilityID: "grype", CapabilityVersion: "1.0.0", TargetDir: "/repo", Actor: "human", Runner: "local", Queued: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.CreatePlan(ctx, model.Plan{ProjectID: proj.ID, PlaybookID: "onboarding", Status: "running"}); err != nil {
		t.Fatal(err)
	}

	var got struct {
		Tasks []model.Task `json:"tasks"`
		Plans []model.Plan `json:"plans"`
	}
	postGet(t, srv.URL+"/v1/activity", &got)
	if len(got.Tasks) != 1 || got.Tasks[0].CapabilityID != "grype" {
		t.Fatalf("tasks = %+v, want one pending grype task", got.Tasks)
	}
	if len(got.Plans) != 1 || got.Plans[0].PlaybookID != "onboarding" {
		t.Fatalf("plans = %+v, want one running onboarding plan", got.Plans)
	}
}
