package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/opensecbench/opensecbench/migrations"
	"github.com/opensecbench/opensecbench/pkg/audit"
)

func TestAuditChainAndResume(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "audit.db")

	db := openAt(t, path)
	e1, err := db.AppendAudit(ctx, "human:james", "task.run", "t1", json.RawMessage(`{"capability":"semgrep"}`))
	if err != nil {
		t.Fatal(err)
	}
	e2, err := db.AppendAudit(ctx, "human:james", "repeater.send", "https://x", nil)
	if err != nil {
		t.Fatal(err)
	}
	if e1.Seq != 1 || e2.Seq != 2 {
		t.Fatalf("seqs = %d,%d, want 1,2", e1.Seq, e2.Seq)
	}
	if e2.PrevHash != e1.Hash {
		t.Fatalf("chain broken: e2.prev=%s e1.hash=%s", e2.PrevHash, e1.Hash)
	}
	// Hash recomputes deterministically.
	if e1.Hash != audit.ChainHash(e1.PrevHash, e1.Seq, e1.Time, e1.Actor, e1.Action, e1.Target, e1.Data) {
		t.Fatal("e1 hash does not verify")
	}

	// Reopen: the chain resumes from the persisted head.
	_ = db.Close()
	db2 := openAt(t, path)
	e3, err := db2.AppendAudit(ctx, "thread:analyst", "analyst.tool", "run_capability", nil)
	if err != nil {
		t.Fatal(err)
	}
	if e3.Seq != 3 || e3.PrevHash != e2.Hash {
		t.Fatalf("resume failed: seq=%d prev=%s want seq=3 prev=%s", e3.Seq, e3.PrevHash, e2.Hash)
	}

	// List returns newest first.
	events, err := db2.ListAudit(ctx, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 3 || events[0].Seq != 3 || events[2].Seq != 1 {
		t.Fatalf("list order wrong: %+v", events)
	}
}

// openAt opens+migrates a database at a fixed path (so it can be reopened).
func openAt(t *testing.T, path string) *DB {
	t.Helper()
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	ms, _ := LoadMigrations(migrations.FS)
	if _, err := db.Apply(ms); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}
