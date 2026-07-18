package audit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestAppendChainsEvents(t *testing.T) {
	var buf bytes.Buffer
	l := New(&buf)
	fixed := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return fixed }

	e1, err := l.Append("human:james", "task.run", "capability:semgrep", nil)
	if err != nil {
		t.Fatal(err)
	}
	e2, err := l.Append("thread:analyst-1", "finding.create", "finding:F-003", json.RawMessage(`{"severity":"high"}`))
	if err != nil {
		t.Fatal(err)
	}

	if e1.Seq != 1 || e2.Seq != 2 {
		t.Fatalf("sequence not monotonic: %d, %d", e1.Seq, e2.Seq)
	}
	if e1.PrevHash != "" {
		t.Fatalf("first event should have empty prev hash, got %q", e1.PrevHash)
	}
	if e2.PrevHash != e1.Hash {
		t.Fatalf("chain broken: e2.PrevHash=%s want %s", e2.PrevHash, e1.Hash)
	}
	if e1.Hash == e2.Hash {
		t.Fatal("distinct events produced identical hashes")
	}

	// Every appended event is a decodable JSON line.
	sc := bufio.NewScanner(&buf)
	n := 0
	for sc.Scan() {
		var e Event
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			t.Fatalf("line %d not valid JSON: %v", n, err)
		}
		n++
	}
	if n != 2 {
		t.Fatalf("wrote %d lines, want 2", n)
	}
}

func TestChainHashIsDeterministic(t *testing.T) {
	e := Event{Seq: 1, Time: time.Unix(0, 0).UTC(), Actor: "a", Action: "b", Target: "c"}
	if chainHash(e) != chainHash(e) {
		t.Fatal("chainHash is not deterministic")
	}
}
