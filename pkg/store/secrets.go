package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// SetSecret upserts a sealed secret value by name. The caller seals the plaintext via the vault;
// this layer only persists ciphertext.
func (db *DB) SetSecret(ctx context.Context, name, sealed string) (model.Secret, error) {
	if name == "" || sealed == "" {
		return model.Secret{}, errors.New("store: secret name and sealed value required")
	}
	ts := nowString()
	id := uuid.NewString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO secrets (id, name, sealed, created_at, updated_at) VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(name) DO UPDATE SET sealed = excluded.sealed, updated_at = excluded.updated_at`,
		id, name, sealed, ts, ts); err != nil {
		return model.Secret{}, err
	}
	return db.GetSecretMeta(ctx, name)
}

// GetSealed returns the sealed (encrypted) value for a name, for the vault to open at exec time.
func (db *DB) GetSealed(ctx context.Context, name string) (string, error) {
	var sealed string
	err := db.QueryRowContext(ctx, `SELECT sealed FROM secrets WHERE name = ?`, name).Scan(&sealed)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return sealed, err
}

// GetSecretMeta returns a secret's metadata (never its value).
func (db *DB) GetSecretMeta(ctx context.Context, name string) (model.Secret, error) {
	var s model.Secret
	var created, updated string
	err := db.QueryRowContext(ctx,
		`SELECT id, name, created_at, updated_at FROM secrets WHERE name = ?`, name).
		Scan(&s.ID, &s.Name, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Secret{}, ErrNotFound
	}
	if err != nil {
		return model.Secret{}, err
	}
	s.CreatedAt, s.UpdatedAt = parseTime(created), parseTime(updated)
	return s, nil
}

// ListSecrets returns metadata for all secrets (names only).
func (db *DB) ListSecrets(ctx context.Context) ([]model.Secret, error) {
	rows, err := db.QueryContext(ctx, `SELECT id, name, created_at, updated_at FROM secrets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Secret
	for rows.Next() {
		var s model.Secret
		var created, updated string
		if err := rows.Scan(&s.ID, &s.Name, &created, &updated); err != nil {
			return nil, err
		}
		s.CreatedAt, s.UpdatedAt = parseTime(created), parseTime(updated)
		out = append(out, s)
	}
	return out, rows.Err()
}

// DeleteSecret removes a secret by name.
func (db *DB) DeleteSecret(ctx context.Context, name string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM secrets WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// AllSecretValues opens every secret via the provided opener — used to build the redaction set
// before persisting task output (ADR-0011). Values that fail to open are skipped.
func (db *DB) AllSecretValues(ctx context.Context, open func(sealed string) ([]byte, error)) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT sealed FROM secrets`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var sealed string
		if err := rows.Scan(&sealed); err != nil {
			return nil, err
		}
		if v, err := open(sealed); err == nil && len(v) > 0 {
			out = append(out, string(v))
		}
	}
	return out, rows.Err()
}
