package store

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func TestThreadMessagesAndFork(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	th, err := db.CreateThread(ctx, NewThread{Title: "IDOR hunt", Provider: "mock"})
	if err != nil {
		t.Fatal(err)
	}
	for i, c := range []string{"system prompt", "user question", "assistant answer"} {
		m, err := db.AppendMessage(ctx, th.ID, "role", c)
		if err != nil {
			t.Fatal(err)
		}
		if m.Seq != i {
			t.Fatalf("message seq = %d, want %d", m.Seq, i)
		}
	}

	msgs, err := db.ListMessages(ctx, th.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(msgs) != 3 {
		t.Fatalf("got %d messages, want 3", len(msgs))
	}

	// Fork at seq 1 -> child has messages 0 and 1 only.
	child, err := db.ForkThread(ctx, th.ID, 1)
	if err != nil {
		t.Fatal(err)
	}
	if child.ParentThreadID == nil || *child.ParentThreadID != th.ID || child.ForkSeq == nil || *child.ForkSeq != 1 {
		t.Fatalf("fork lineage wrong: %+v", child)
	}
	childMsgs, err := db.ListMessages(ctx, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(childMsgs) != 2 {
		t.Fatalf("fork copied %d messages, want 2", len(childMsgs))
	}
}

func TestArchiveAndDeleteThread(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	keep, _ := db.CreateThread(ctx, NewThread{Title: "keep"})
	arch, _ := db.CreateThread(ctx, NewThread{Title: "archive me"})
	if _, err := db.AppendMessage(ctx, arch.ID, "user", "sensitive finding"); err != nil {
		t.Fatal(err)
	}

	// Archive drops the thread from the active list but retains it (transcript intact) for auditability.
	if err := db.ArchiveThread(ctx, arch.ID); err != nil {
		t.Fatal(err)
	}
	active, err := db.ListThreads(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(active) != 1 || active[0].ID != keep.ID {
		t.Fatalf("active list = %+v, want only %s", active, keep.ID)
	}
	got, err := db.GetThread(ctx, arch.ID)
	if err != nil {
		t.Fatalf("archived thread not retrievable: %v", err)
	}
	if got.ArchivedAt == nil {
		t.Fatal("archived_at not set on archived thread")
	}
	if msgs, _ := db.ListMessages(ctx, arch.ID); len(msgs) != 1 {
		t.Fatalf("archived transcript lost: %d messages, want 1", len(msgs))
	}

	// Archiving is idempotent; a missing thread is ErrNotFound.
	if err := db.ArchiveThread(ctx, arch.ID); err != nil {
		t.Fatalf("re-archive: %v", err)
	}
	if err := db.ArchiveThread(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("archive missing = %v, want ErrNotFound", err)
	}

	// Delete is the permanent purge: messages cascade, the thread is gone.
	if err := db.DeleteThread(ctx, arch.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.GetThread(ctx, arch.ID); err != ErrNotFound {
		t.Fatalf("purged thread still present: %v", err)
	}
	if msgs, _ := db.ListMessages(ctx, arch.ID); len(msgs) != 0 {
		t.Fatalf("purge left %d orphan messages", len(msgs))
	}
	if err := db.DeleteThread(ctx, "nope"); err != ErrNotFound {
		t.Fatalf("delete missing = %v, want ErrNotFound", err)
	}
}

func TestApprovalQueue(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()
	th, _ := db.CreateThread(ctx, NewThread{Title: "t"})

	ap, err := db.CreateApproval(ctx, th.ID, "run_capability", json.RawMessage(`{"capability":"nuclei"}`))
	if err != nil {
		t.Fatal(err)
	}
	if ap.Status != model.ApprovalPending {
		t.Fatalf("status = %s", ap.Status)
	}

	pending, err := db.ListPendingApprovals(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 1 {
		t.Fatalf("pending = %d, want 1", len(pending))
	}

	decided, err := db.DecideApproval(ctx, ap.ID, model.ApprovalApproved)
	if err != nil {
		t.Fatal(err)
	}
	if decided.Status != model.ApprovalApproved || decided.DecidedAt == nil {
		t.Fatalf("decided wrong: %+v", decided)
	}

	// Deciding again fails.
	if _, err := db.DecideApproval(ctx, ap.ID, model.ApprovalDenied); err == nil {
		t.Fatal("expected error deciding an already-decided approval")
	}
}
