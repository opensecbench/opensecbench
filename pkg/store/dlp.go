package store

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// CreateCanary generates a unique decoy token with the given label and stores it.
func (db *DB) CreateCanary(ctx context.Context, label string) (model.Canary, error) {
	if label == "" {
		return model.Canary{}, errors.New("store: canary label required")
	}
	buf := make([]byte, 12)
	if _, err := rand.Read(buf); err != nil {
		return model.Canary{}, err
	}
	c := model.Canary{ID: uuid.NewString(), Label: label, Token: "OSB-CANARY-" + hex.EncodeToString(buf)}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO canaries (id, label, token, created_at) VALUES (?, ?, ?, ?)`,
		c.ID, c.Label, c.Token, ts); err != nil {
		return model.Canary{}, err
	}
	c.CreatedAt = parseTime(ts)
	return c, nil
}

// ListCanaries returns all planted canaries.
func (db *DB) ListCanaries(ctx context.Context) ([]model.Canary, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, label, token, created_at FROM canaries ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Canary
	for rows.Next() {
		var c model.Canary
		var created string
		if err := rows.Scan(&c.ID, &c.Label, &c.Token, &created); err != nil {
			return nil, err
		}
		c.CreatedAt = parseTime(created)
		out = append(out, c)
	}
	return out, rows.Err()
}

// CanaryMap returns token -> label, for the DLP scanner.
func (db *DB) CanaryMap(ctx context.Context) (map[string]string, error) {
	cs, err := db.ListCanaries(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]string, len(cs))
	for _, c := range cs {
		m[c.Token] = c.Label
	}
	return m, nil
}

// DeleteCanary removes a canary by id.
func (db *DB) DeleteCanary(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM canaries WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SecretValueMap returns value -> name for all secrets (opened via the vault), for DLP scanning.
func (db *DB) SecretValueMap(ctx context.Context, open func(sealed string) ([]byte, error)) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT name, sealed FROM secrets`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	m := map[string]string{}
	for rows.Next() {
		var name, sealed string
		if err := rows.Scan(&name, &sealed); err != nil {
			return nil, err
		}
		if v, err := open(sealed); err == nil && len(v) > 0 {
			m[string(v)] = name
		}
	}
	return m, rows.Err()
}

// RecordDLPEvent appends a DLP event to the trail.
func (db *DB) RecordDLPEvent(ctx context.Context, e model.DLPEvent) error {
	blocked := 0
	if e.Blocked {
		blocked = 1
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO dlp_events (id, kind, label, action, blocked, location, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), e.Kind, e.Label, e.Action, blocked, e.Location, nowString())
	return err
}

// ListDLPEvents returns recent DLP events, newest first.
func (db *DB) ListDLPEvents(ctx context.Context, limit int) ([]model.DLPEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, kind, label, action, blocked, location, created_at FROM dlp_events ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.DLPEvent
	for rows.Next() {
		var e model.DLPEvent
		var blocked int
		var created string
		if err := rows.Scan(&e.ID, &e.Kind, &e.Label, &e.Action, &blocked, &e.Location, &created); err != nil {
			return nil, err
		}
		e.Blocked = blocked != 0
		e.CreatedAt = parseTime(created)
		out = append(out, e)
	}
	return out, rows.Err()
}
