package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// ListDispositionRules returns a project's disposition overrides, highest priority first (ADR-0028).
func (db *DB) ListDispositionRules(ctx context.Context, projectID string) ([]model.DispositionRule, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, capability_id, when_json, min_severity, action, priority, created_at
		 FROM disposition_rules WHERE project_id = ? ORDER BY priority DESC, created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.DispositionRule
	for rows.Next() {
		var r model.DispositionRule
		var whenJSON, created string
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.CapabilityID, &whenJSON, &r.MinSeverity, &r.Action, &r.Priority, &created); err != nil {
			return nil, err
		}
		if whenJSON != "" {
			_ = json.Unmarshal([]byte(whenJSON), &r.When)
		}
		r.CreatedAt = parseTime(created)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetDispositionRule inserts a project disposition override.
func (db *DB) SetDispositionRule(ctx context.Context, r model.DispositionRule) (model.DispositionRule, error) {
	r.ID = uuid.NewString()
	r.CreatedAt = time.Now().UTC()
	when := "{}"
	if len(r.When) > 0 {
		b, _ := json.Marshal(r.When)
		when = string(b)
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO disposition_rules (id, project_id, capability_id, when_json, min_severity, action, priority, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ProjectID, r.CapabilityID, when, r.MinSeverity, r.Action, r.Priority, r.CreatedAt.Format(timeLayout))
	if err != nil {
		return model.DispositionRule{}, err
	}
	return r, nil
}

// DeleteDispositionRule removes a project disposition override.
func (db *DB) DeleteDispositionRule(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM disposition_rules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
