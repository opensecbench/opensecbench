package store

import (
	"context"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestSessionLifecycle(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	proj, err := db.CreateProject(ctx, NewProject{Name: "engagement"})
	if err != nil {
		t.Fatal(err)
	}

	s, err := db.CreateSession(ctx, model.Session{
		ProjectID: proj.ID,
		Runner:    "local-docker",
		Container: "osb-sess-1",
		Image:     "alpine:3",
		Actor:     "human:james",
	})
	if err != nil {
		t.Fatal(err)
	}
	if s.Status != model.SessionActive || s.Kind != model.SessionTerminal || s.CreatedAt.IsZero() {
		t.Fatalf("new session wrong: %+v", s)
	}

	// An active session appears in the project list.
	list, err := db.ListSessionsByProject(ctx, proj.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != s.ID {
		t.Fatalf("list = %+v, want the one session", list)
	}

	// Close with a transcript artifact.
	art, err := db.CreateArtifact(ctx, model.Artifact{SHA256: "deadbeef", Size: 3, Kind: model.ArtifactInput, Name: "transcript"})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.CloseSession(ctx, s.ID, model.SessionClosed, &art.ID, ""); err != nil {
		t.Fatal(err)
	}

	got, err := db.GetSession(ctx, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != model.SessionClosed || got.ClosedAt == nil {
		t.Fatalf("session not closed: %+v", got)
	}
	if got.TranscriptArtifactID == nil || *got.TranscriptArtifactID != art.ID {
		t.Fatalf("transcript artifact not attached: %+v", got)
	}

	// Closing an already-closed session is a no-op miss.
	if err := db.CloseSession(ctx, s.ID, model.SessionClosed, nil, ""); err != ErrNotFound {
		t.Fatalf("re-close = %v, want ErrNotFound", err)
	}
}
