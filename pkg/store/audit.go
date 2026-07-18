package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/opensecbench/opensecbench/pkg/audit"
	"github.com/opensecbench/opensecbench/pkg/model"
)

// AppendAudit records a hash-chained audit event. It reads the current chain head, links the new
// event to it, and inserts — all under a mutex so the chain stays consistent under concurrency.
// The chain head is recovered from the table on every call, so it survives restarts.
func (db *DB) AppendAudit(ctx context.Context, actor, action, target string, data json.RawMessage) (model.AuditEvent, error) {
	db.auditMu.Lock()
	defer db.auditMu.Unlock()

	var seq sql.NullInt64
	var prev sql.NullString
	err := db.QueryRowContext(ctx, `SELECT seq, hash FROM audit_events ORDER BY seq DESC LIMIT 1`).Scan(&seq, &prev)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return model.AuditEvent{}, err
	}

	e := model.AuditEvent{
		Seq:      uint64(seq.Int64) + 1,
		Time:     time.Now().UTC(),
		Actor:    actor,
		Action:   action,
		Target:   target,
		Data:     data,
		PrevHash: prev.String,
	}
	e.Hash = audit.ChainHash(e.PrevHash, e.Seq, e.Time, e.Actor, e.Action, e.Target, e.Data)

	if _, err := db.ExecContext(ctx,
		`INSERT INTO audit_events (seq, time, actor, action, target, data, prev_hash, hash)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.Seq, e.Time.Format(timeLayout), e.Actor, e.Action, e.Target, string(e.Data), e.PrevHash, e.Hash); err != nil {
		return model.AuditEvent{}, err
	}
	return e, nil
}

// VerifyAuditChain walks the audit trail in order, recomputing each event's hash and checking the
// prev-hash linkage. It returns ok=false and the first broken seq if tampering is detected.
func (db *DB) VerifyAuditChain(ctx context.Context) (ok bool, brokenSeq uint64, count int, err error) {
	rows, err := db.QueryContext(ctx,
		`SELECT seq, time, actor, action, target, data, prev_hash, hash FROM audit_events ORDER BY seq`)
	if err != nil {
		return false, 0, 0, err
	}
	defer func() { _ = rows.Close() }()

	prev := ""
	for rows.Next() {
		var e model.AuditEvent
		var t, data string
		if err := rows.Scan(&e.Seq, &t, &e.Actor, &e.Action, &e.Target, &data, &e.PrevHash, &e.Hash); err != nil {
			return false, 0, count, err
		}
		e.Time = parseTime(t)
		want := audit.ChainHash(e.PrevHash, e.Seq, e.Time, e.Actor, e.Action, e.Target, json.RawMessage(data))
		if e.PrevHash != prev || e.Hash != want {
			return false, e.Seq, count, nil
		}
		prev = e.Hash
		count++
	}
	return true, 0, count, rows.Err()
}

// ListAudit returns the most recent audit events, newest first.
func (db *DB) ListAudit(ctx context.Context, limit int) ([]model.AuditEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx,
		`SELECT seq, time, actor, action, target, data, prev_hash, hash
		 FROM audit_events ORDER BY seq DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.AuditEvent
	for rows.Next() {
		var e model.AuditEvent
		var t, data string
		if err := rows.Scan(&e.Seq, &t, &e.Actor, &e.Action, &e.Target, &data, &e.PrevHash, &e.Hash); err != nil {
			return nil, err
		}
		e.Time = parseTime(t)
		if data != "" {
			e.Data = json.RawMessage(data)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
