package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// CreateProvider registers an LLM provider. KeySealed (if any) must already be vault-sealed.
func (db *DB) CreateProvider(ctx context.Context, p model.Provider) (model.Provider, error) {
	if p.Name == "" || p.Type == "" {
		return model.Provider{}, fmt.Errorf("store: provider name and type required")
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO providers (id, name, type, model, base_url, key_sealed, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Type, p.Model, p.BaseURL, p.KeySealed, ts); err != nil {
		return model.Provider{}, err
	}
	p.CreatedAt = parseTime(ts)
	return p, nil
}

const providerCols = `id, name, type, model, base_url, key_sealed, created_at, models_refreshed_at`

func scanProvider(s interface{ Scan(...any) error }) (model.Provider, error) {
	var p model.Provider
	var created, refreshed string
	if err := s.Scan(&p.ID, &p.Name, &p.Type, &p.Model, &p.BaseURL, &p.KeySealed, &created, &refreshed); err != nil {
		return model.Provider{}, err
	}
	p.CreatedAt = parseTime(created)
	if refreshed != "" {
		p.ModelsRefreshedAt = parseTime(refreshed)
	}
	return p, nil
}

// ListProviders returns all registered providers, oldest first.
func (db *DB) ListProviders(ctx context.Context) ([]model.Provider, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+providerCols+` FROM providers ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Provider
	for rows.Next() {
		p, err := scanProvider(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// GetProvider returns one provider by id.
func (db *DB) GetProvider(ctx context.Context, id string) (model.Provider, error) {
	row := db.QueryRowContext(ctx, `SELECT `+providerCols+` FROM providers WHERE id = ?`, id)
	p, err := scanProvider(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Provider{}, ErrNotFound
	}
	return p, err
}

// DeleteProvider removes a provider.
func (db *DB) DeleteProvider(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM providers WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
