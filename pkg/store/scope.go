package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

var validScopeKinds = map[string]bool{"host": true, "domain": true, "cidr": true}

// AddScopeEntry adds an in-scope allowlist rule to a project.
func (db *DB) AddScopeEntry(ctx context.Context, projectID, kind, value string) (model.ScopeEntry, error) {
	if projectID == "" || value == "" {
		return model.ScopeEntry{}, fmt.Errorf("store: scope entry project id and value required")
	}
	if !validScopeKinds[kind] {
		return model.ScopeEntry{}, fmt.Errorf("store: invalid scope kind %q (host|domain|cidr)", kind)
	}
	e := model.ScopeEntry{ID: uuid.NewString(), ProjectID: projectID, Kind: kind, Value: value}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO scope_entries (id, project_id, kind, value, created_at) VALUES (?, ?, ?, ?, ?)`,
		e.ID, projectID, kind, value, ts); err != nil {
		return model.ScopeEntry{}, err
	}
	e.CreatedAt = parseTime(ts)
	return e, nil
}

// ListScopeEntries returns a project's scope entries.
func (db *DB) ListScopeEntries(ctx context.Context, projectID string) ([]model.ScopeEntry, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, kind, value, created_at FROM scope_entries WHERE project_id = ? ORDER BY created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.ScopeEntry
	for rows.Next() {
		var e model.ScopeEntry
		var created string
		if err := rows.Scan(&e.ID, &e.ProjectID, &e.Kind, &e.Value, &created); err != nil {
			return nil, err
		}
		e.CreatedAt = parseTime(created)
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteScopeEntry removes a scope entry.
func (db *DB) DeleteScopeEntry(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM scope_entries WHERE id = ?`, id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}
