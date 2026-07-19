package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestProjectExposure(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	// A fresh project with no evidence is not exposed.
	proj, err := db.CreateProject(ctx, NewProject{Name: "p"})
	if err != nil {
		t.Fatal(err)
	}
	if exp, _ := db.ProjectExposure(ctx, proj.ID); exp.Exposed {
		t.Fatalf("empty project should not be exposed: %+v", exp)
	}

	// An nmap open-port observation marks it exposed.
	if _, err := db.CreateObservation(ctx, model.Observation{
		ProjectID: &proj.ID, Origin: model.OriginTool, Title: "open", RuleID: "nmap/open-port",
		Location: "10.0.0.1:443/tcp",
	}); err != nil {
		t.Fatal(err)
	}
	exp, _ := db.ProjectExposure(ctx, proj.ID)
	if !exp.Exposed || len(exp.OpenPorts) != 1 || exp.OpenPorts[0] != "10.0.0.1:443/tcp" {
		t.Fatalf("open-port should expose the project: %+v", exp)
	}

	// A cloud_deployment asset is an independent exposure signal.
	proj2, _ := db.CreateProject(ctx, NewProject{Name: "p2"})
	app, _ := db.CreateApplication(ctx, proj2.ID, "app")
	if _, err := db.CreateAsset(ctx, NewAsset{
		ApplicationID: app.ID, Type: model.AssetCloudDeployment, Location: "prod-cluster",
	}); err != nil {
		t.Fatal(err)
	}
	exp2, _ := db.ProjectExposure(ctx, proj2.ID)
	if !exp2.Exposed || len(exp2.Deployments) != 1 || exp2.Deployments[0] != "prod-cluster" {
		t.Fatalf("cloud_deployment should expose the project: %+v", exp2)
	}
}
