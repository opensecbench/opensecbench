package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

var validRuleTarget = map[string]bool{
	model.RuleTargetURL:            true,
	model.RuleTargetRequestHeader:  true,
	model.RuleTargetRequestBody:    true,
	model.RuleTargetResponseHeader: true,
	model.RuleTargetResponseBody:   true,
}

// CreateProxyRule persists a match/replace rule for a project.
func (db *DB) CreateProxyRule(ctx context.Context, r model.ProxyRule) (model.ProxyRule, error) {
	if r.ProjectID == "" || r.Match == "" {
		return model.ProxyRule{}, fmt.Errorf("store: proxy rule project id and match required")
	}
	if !validRuleTarget[r.Target] {
		return model.ProxyRule{}, fmt.Errorf("store: invalid proxy rule target %q", r.Target)
	}
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO proxy_rules (id, project_id, enabled, target, match, replace, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ProjectID, r.Enabled, r.Target, r.Match, r.Replace, ts); err != nil {
		return model.ProxyRule{}, err
	}
	r.CreatedAt = parseTime(ts)
	return r, nil
}

// ListProxyRules returns a project's rules, oldest first (application order).
func (db *DB) ListProxyRules(ctx context.Context, projectID string) ([]model.ProxyRule, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, enabled, target, match, replace, created_at
		 FROM proxy_rules WHERE project_id = ? ORDER BY created_at ASC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.ProxyRule
	for rows.Next() {
		var r model.ProxyRule
		var created string
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Enabled, &r.Target, &r.Match, &r.Replace, &created); err != nil {
			return nil, err
		}
		r.CreatedAt = parseTime(created)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetProxyRuleEnabled toggles a rule and returns the owning project id (so the caller can rebuild it).
func (db *DB) SetProxyRuleEnabled(ctx context.Context, id string, enabled bool) (string, error) {
	var projectID string
	if err := db.QueryRowContext(ctx, `SELECT project_id FROM proxy_rules WHERE id = ?`, id).Scan(&projectID); err != nil {
		return "", ErrNotFound
	}
	_, err := db.ExecContext(ctx, `UPDATE proxy_rules SET enabled = ? WHERE id = ?`, enabled, id)
	return projectID, err
}

// DeleteProxyRule removes a rule and returns the owning project id.
func (db *DB) DeleteProxyRule(ctx context.Context, id string) (string, error) {
	var projectID string
	if err := db.QueryRowContext(ctx, `SELECT project_id FROM proxy_rules WHERE id = ?`, id).Scan(&projectID); err != nil {
		return "", ErrNotFound
	}
	_, err := db.ExecContext(ctx, `DELETE FROM proxy_rules WHERE id = ?`, id)
	return projectID, err
}
