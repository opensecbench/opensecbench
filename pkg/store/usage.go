package store

import (
	"context"
	"database/sql"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// RecordUsage persists one run's token usage. Zero-token runs are skipped (nothing to compare).
func (db *DB) RecordUsage(ctx context.Context, u model.UsageRecord) error {
	if u.InputTokens == 0 && u.OutputTokens == 0 {
		return nil
	}
	if u.ID == "" {
		u.ID = uuid.NewString()
	}
	var projectID any
	if u.ProjectID != "" {
		projectID = u.ProjectID
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO usage_records (id, project_id, thread_id, provider, model, input_tokens, output_tokens, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, projectID, u.ThreadID, u.Provider, u.Model, u.InputTokens, u.OutputTokens, nowString())
	return err
}

// UsageByModel returns token totals grouped by (provider, model) for a project, most-used first.
func (db *DB) UsageByModel(ctx context.Context, projectID string) ([]model.UsageByModel, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT provider, model, COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)
		 FROM usage_records WHERE project_id = ?
		 GROUP BY provider, model
		 ORDER BY SUM(input_tokens) + SUM(output_tokens) DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.UsageByModel
	for rows.Next() {
		var u model.UsageByModel
		var mdl sql.NullString
		if err := rows.Scan(&u.Provider, &mdl, &u.Runs, &u.InputTokens, &u.OutputTokens); err != nil {
			return nil, err
		}
		u.Model = mdl.String
		out = append(out, u)
	}
	return out, rows.Err()
}
