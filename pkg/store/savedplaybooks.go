package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// CreateSavedPlaybook stores a user-saved agent playbook.
func (db *DB) CreateSavedPlaybook(ctx context.Context, p model.SavedPlaybook) (model.SavedPlaybook, error) {
	if p.Name == "" || len(p.Steps) == 0 {
		return model.SavedPlaybook{}, errors.New("store: saved playbook needs a name and steps")
	}
	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO saved_playbooks (id, name, description, goal, steps, source, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Name, p.Description, p.Goal, string(p.Steps), p.Source, ts); err != nil {
		return model.SavedPlaybook{}, err
	}
	p.CreatedAt = parseTime(ts)
	return p, nil
}

// GetSavedPlaybook returns one saved playbook by id.
func (db *DB) GetSavedPlaybook(ctx context.Context, id string) (model.SavedPlaybook, error) {
	var p model.SavedPlaybook
	var steps, created string
	err := db.QueryRowContext(ctx,
		`SELECT id, name, description, goal, steps, source, created_at FROM saved_playbooks WHERE id = ?`, id).
		Scan(&p.ID, &p.Name, &p.Description, &p.Goal, &steps, &p.Source, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return model.SavedPlaybook{}, ErrNotFound
	}
	if err != nil {
		return model.SavedPlaybook{}, err
	}
	p.Steps = []byte(steps)
	p.CreatedAt = parseTime(created)
	return p, nil
}

// ListSavedPlaybooks returns all saved playbooks, newest first.
func (db *DB) ListSavedPlaybooks(ctx context.Context) ([]model.SavedPlaybook, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, description, goal, steps, source, created_at FROM saved_playbooks ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.SavedPlaybook
	for rows.Next() {
		var p model.SavedPlaybook
		var steps, created string
		if err := rows.Scan(&p.ID, &p.Name, &p.Description, &p.Goal, &steps, &p.Source, &created); err != nil {
			return nil, err
		}
		p.Steps = []byte(steps)
		p.CreatedAt = parseTime(created)
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteSavedPlaybook removes a saved playbook.
func (db *DB) DeleteSavedPlaybook(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM saved_playbooks WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
