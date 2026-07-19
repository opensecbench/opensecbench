package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// CreateSavedProfile stores a user-defined agent profile.
func (db *DB) CreateSavedProfile(ctx context.Context, p model.SavedProfile) (model.SavedProfile, error) {
	if p.Name == "" || p.Persona == "" || len(p.Tools) == 0 {
		return model.SavedProfile{}, errors.New("store: saved profile needs a name, persona, and tools")
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO saved_profiles (id, name, description, persona, tools, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Description, p.Persona, string(p.Tools), ts); err != nil {
		return model.SavedProfile{}, err
	}
	p.CreatedAt = parseTime(ts)
	return p, nil
}

// GetSavedProfile returns one saved profile by id.
func (db *DB) GetSavedProfile(ctx context.Context, id string) (model.SavedProfile, error) {
	var p model.SavedProfile
	var tools, created string
	err := db.QueryRowContext(ctx,
		`SELECT id, name, description, persona, tools, created_at FROM saved_profiles WHERE id = ?`, id).
		Scan(&p.ID, &p.Name, &p.Description, &p.Persona, &tools, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return model.SavedProfile{}, ErrNotFound
	}
	if err != nil {
		return model.SavedProfile{}, err
	}
	p.Tools = []byte(tools)
	p.CreatedAt = parseTime(created)
	return p, nil
}

// ListSavedProfiles returns all saved profiles, newest first.
func (db *DB) ListSavedProfiles(ctx context.Context) ([]model.SavedProfile, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, description, persona, tools, created_at FROM saved_profiles ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.SavedProfile
	for rows.Next() {
		var p model.SavedProfile
		var tools, created string
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Persona, &tools, &created); err != nil {
			return nil, err
		}
		p.Tools = []byte(tools)
		p.CreatedAt = parseTime(created)
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteSavedProfile removes a saved profile.
func (db *DB) DeleteSavedProfile(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM saved_profiles WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
