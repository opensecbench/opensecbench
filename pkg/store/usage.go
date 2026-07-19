package store

import (
	"context"
	"database/sql"
	"time"

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

// UsageSummary rolls up token spend across the whole workbench for the Home cockpit: this-month
// (records at/after monthStart) and all-time totals, plus the heaviest (provider, model) pairs.
// The month boundary is compared against the RFC3339 created_at string; records within a
// sub-second of midnight on the first may fall on either side, which is immaterial for a spend view.
func (db *DB) UsageSummary(ctx context.Context, monthStart time.Time, topN int) (model.UsageSummary, error) {
	var s model.UsageSummary
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0) FROM usage_records`,
	).Scan(&s.AllInput, &s.AllOutput); err != nil {
		return s, err
	}
	if err := db.QueryRowContext(ctx,
		`SELECT COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0) FROM usage_records WHERE created_at >= ?`,
		monthStart.UTC().Format(timeLayout),
	).Scan(&s.MonthInput, &s.MonthOutput); err != nil {
		return s, err
	}
	rows, err := db.QueryContext(ctx,
		`SELECT provider, model, COUNT(*), COALESCE(SUM(input_tokens),0), COALESCE(SUM(output_tokens),0)
		 FROM usage_records
		 GROUP BY provider, model
		 ORDER BY SUM(input_tokens) + SUM(output_tokens) DESC
		 LIMIT ?`, topN)
	if err != nil {
		return s, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var u model.UsageByModel
		var mdl sql.NullString
		if err := rows.Scan(&u.Provider, &mdl, &u.Runs, &u.InputTokens, &u.OutputTokens); err != nil {
			return s, err
		}
		u.Model = mdl.String
		s.TopModels = append(s.TopModels, u)
	}
	return s, rows.Err()
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
