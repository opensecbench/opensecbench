// Package audit provides an append-only, hash-chained event log.
//
// Every control-plane action is recorded here (ADR-0001, ADR-0006). Each event links to the
// previous one by hash, so any tampering with earlier entries is detectable.
package audit

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Event is one immutable audit record.
type Event struct {
	Seq      uint64          `json:"seq"`
	Time     time.Time       `json:"time"`
	Actor    string          `json:"actor"`
	Action   string          `json:"action"`
	Target   string          `json:"target,omitempty"`
	Data     json.RawMessage `json:"data,omitempty"`
	PrevHash string          `json:"prev_hash"`
	Hash     string          `json:"hash"`
}

// Log appends hash-chained events to an underlying writer (JSON, one event per line).
type Log struct {
	mu       sync.Mutex
	enc      *json.Encoder
	seq      uint64
	prevHash string
	now      func() time.Time
}

// New returns a Log that writes to w.
func New(w io.Writer) *Log {
	return &Log{enc: json.NewEncoder(w), now: time.Now}
}

// Append records an action and returns the written event. It is safe for concurrent use.
func (l *Log) Append(actor, action, target string, data json.RawMessage) (Event, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	e := Event{
		Seq:      l.seq + 1,
		Time:     l.now().UTC(),
		Actor:    actor,
		Action:   action,
		Target:   target,
		Data:     data,
		PrevHash: l.prevHash,
	}
	e.Hash = chainHash(e)

	if err := l.enc.Encode(e); err != nil {
		return Event{}, err
	}
	l.seq = e.Seq
	l.prevHash = e.Hash
	return e, nil
}

// chainHash binds an event to its predecessor over a stable field encoding.
func chainHash(e Event) string {
	return ChainHash(e.PrevHash, e.Seq, e.Time, e.Actor, e.Action, e.Target, e.Data)
}

// ChainHash computes an event's hash from its predecessor's hash and its fields. Any persistence
// backend (file or database) uses this so the tamper-evidence chain is identical everywhere.
func ChainHash(prevHash string, seq uint64, t time.Time, actor, action, target string, data []byte) string {
	header := fmt.Sprintf("%s\n%d\n%s\n%s\n%s\n%s\n",
		prevHash, seq, t.Format(time.RFC3339Nano), actor, action, target)
	h := sha256.New()
	h.Write([]byte(header))
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}
