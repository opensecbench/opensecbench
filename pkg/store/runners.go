package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// CreateRunner records an enrolled remote runner (ADR-0024). PubKey is the base64 ed25519 public key the
// runner will authenticate with.
func (db *DB) CreateRunner(ctx context.Context, name, pubKey string) (model.Runner, error) {
	if name == "" || pubKey == "" {
		return model.Runner{}, errors.New("store: runner name and pubkey required")
	}
	r := model.Runner{
		ID:         uuid.NewString(),
		Name:       name,
		PubKey:     pubKey,
		Status:     model.RunnerActive,
		EnrolledAt: time.Now().UTC(),
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO runners (id, name, pubkey, status, enrolled_at) VALUES (?, ?, ?, ?, ?)`,
		r.ID, r.Name, r.PubKey, r.Status, r.EnrolledAt.Format(timeLayout)); err != nil {
		return model.Runner{}, err
	}
	return r, nil
}

const runnerCols = `id, name, pubkey, status, enrolled_at, last_seen`

func scanRunner(s interface{ Scan(...any) error }) (model.Runner, error) {
	var r model.Runner
	var enrolled string
	var lastSeen sql.NullString
	if err := s.Scan(&r.ID, &r.Name, &r.PubKey, &r.Status, &enrolled, &lastSeen); err != nil {
		return model.Runner{}, err
	}
	r.EnrolledAt = parseTime(enrolled)
	r.LastSeen = ptrTime(lastSeen)
	return r, nil
}

// GetRunner returns one runner by id.
func (db *DB) GetRunner(ctx context.Context, id string) (model.Runner, error) {
	r, err := scanRunner(db.QueryRowContext(ctx, `SELECT `+runnerCols+` FROM runners WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Runner{}, ErrNotFound
	}
	return r, err
}

// ListRunners returns all enrolled runners, oldest first.
func (db *DB) ListRunners(ctx context.Context) ([]model.Runner, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+runnerCols+` FROM runners ORDER BY enrolled_at ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Runner
	for rows.Next() {
		r, err := scanRunner(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// TouchRunner stamps a runner's last_seen (on connect / heartbeat).
func (db *DB) TouchRunner(ctx context.Context, id string) error {
	_, err := db.ExecContext(ctx, `UPDATE runners SET last_seen = ? WHERE id = ?`,
		time.Now().UTC().Format(timeLayout), id)
	return err
}

// DeleteRunner removes an enrolled runner (revoking its access).
func (db *DB) DeleteRunner(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM runners WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MintEnrollToken records a one-time enrollment token by its sha256 hash (hex) with an expiry. The token
// itself is shown to the operator once and never stored.
func (db *DB) MintEnrollToken(ctx context.Context, tokenHash, label string, expiresAt time.Time) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO runner_enroll_tokens (token_hash, label, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		tokenHash, label, time.Now().UTC().Format(timeLayout), expiresAt.UTC().Format(timeLayout))
	return err
}

// ConsumeEnrollToken atomically marks an unused, unexpired token used and reports whether it was valid.
// A token can be redeemed exactly once.
func (db *DB) ConsumeEnrollToken(ctx context.Context, tokenHash string) (bool, error) {
	now := time.Now().UTC().Format(timeLayout)
	res, err := db.ExecContext(ctx,
		`UPDATE runner_enroll_tokens SET used_at = ? WHERE token_hash = ? AND used_at IS NULL AND expires_at > ?`,
		now, tokenHash, now)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}
