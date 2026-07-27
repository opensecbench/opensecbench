package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// CreateSavedMethodology stores a user-authored methodology pack. The caller supplies the id (packs are
// referenced by a stable, human-scoped id, e.g. item ids like "my-pack/idor").
func (db *DB) CreateSavedMethodology(ctx context.Context, m model.SavedMethodology) (model.SavedMethodology, error) {
	if m.ID == "" || m.Title == "" || len(m.Data) == 0 {
		return model.SavedMethodology{}, errors.New("store: saved methodology needs an id, title, and data")
	}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO saved_methodologies (id, title, data, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?)`,
		m.ID, m.Title, string(m.Data), ts, ts); err != nil {
		return model.SavedMethodology{}, err
	}
	m.CreatedAt = parseTime(ts)
	m.UpdatedAt = m.CreatedAt
	return m, nil
}

// UpdateSavedMethodology replaces a saved pack's title and data in place, keeping its id (so adopted-pack and
// coverage references stay valid) and created_at. Returns ErrNotFound if no such saved pack exists — which is
// how built-in and extension packs stay immutable (they have no row here).
func (db *DB) UpdateSavedMethodology(ctx context.Context, m model.SavedMethodology) (model.SavedMethodology, error) {
	if m.ID == "" {
		return model.SavedMethodology{}, errors.New("store: update needs a methodology id")
	}
	if m.Title == "" || len(m.Data) == 0 {
		return model.SavedMethodology{}, errors.New("store: saved methodology needs a title and data")
	}
	ts := nowString()
	res, err := db.ExecContext(ctx,
		`UPDATE saved_methodologies SET title = ?, data = ?, updated_at = ? WHERE id = ?`,
		m.Title, string(m.Data), ts, m.ID)
	if err != nil {
		return model.SavedMethodology{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.SavedMethodology{}, ErrNotFound
	}
	return db.GetSavedMethodology(ctx, m.ID)
}

// GetSavedMethodology returns one saved methodology pack by id.
func (db *DB) GetSavedMethodology(ctx context.Context, id string) (model.SavedMethodology, error) {
	var m model.SavedMethodology
	var data, created, updated string
	err := db.QueryRowContext(ctx,
		`SELECT id, title, data, created_at, updated_at FROM saved_methodologies WHERE id = ?`, id).
		Scan(&m.ID, &m.Title, &data, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return model.SavedMethodology{}, ErrNotFound
	}
	if err != nil {
		return model.SavedMethodology{}, err
	}
	m.Data = []byte(data)
	m.CreatedAt = parseTime(created)
	m.UpdatedAt = parseTime(updated)
	return m, nil
}

// ListSavedMethodologies returns all saved methodology packs, newest first.
func (db *DB) ListSavedMethodologies(ctx context.Context) ([]model.SavedMethodology, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, title, data, created_at, updated_at FROM saved_methodologies ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.SavedMethodology
	for rows.Next() {
		var m model.SavedMethodology
		var data, created, updated string
		if err := rows.Scan(&m.ID, &m.Title, &data, &created, &updated); err != nil {
			return nil, err
		}
		m.Data = []byte(data)
		m.CreatedAt = parseTime(created)
		m.UpdatedAt = parseTime(updated)
		out = append(out, m)
	}
	return out, rows.Err()
}

// DeleteSavedMethodology removes a user-saved methodology pack.
func (db *DB) DeleteSavedMethodology(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM saved_methodologies WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
