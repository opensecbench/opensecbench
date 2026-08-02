package store

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

var validRulePhase = map[string]bool{
	model.RulePhaseRequest:  true,
	model.RulePhaseResponse: true,
	model.RulePhaseBoth:     true,
}
var validRuleAction = map[string]bool{
	model.ActionHold: true, model.ActionDrop: true,
	model.ActionSetHeader: true, model.ActionRemoveHeader: true,
	model.ActionReplaceBody: true, model.ActionSetStatus: true,
}

// ListTrafficRules returns a project's rules in evaluation order (seq ascending).
func (db *DB) ListTrafficRules(ctx context.Context, projectID string) ([]model.TrafficRule, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, seq, enabled, phase, match_expr, action, params, created_at
		 FROM traffic_rules WHERE project_id = ? ORDER BY seq`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.TrafficRule
	for rows.Next() {
		var r model.TrafficRule
		var params, created string
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Seq, &r.Enabled, &r.Phase, &r.Match, &r.Action, &params, &created); err != nil {
			return nil, err
		}
		_ = json.Unmarshal([]byte(params), &r.Params)
		r.CreatedAt = parseTime(created)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ReplaceTrafficRules atomically replaces a project's entire ordered rule list. The caller has already
// validated each rule's CEL match; here we validate phase/action and persist seq = slice index.
func (db *DB) ReplaceTrafficRules(ctx context.Context, projectID string, rules []model.TrafficRule) ([]model.TrafficRule, error) {
	for i, r := range rules {
		if !validRulePhase[r.Phase] {
			return nil, fmt.Errorf("store: rule %d invalid phase %q", i, r.Phase)
		}
		if !validRuleAction[r.Action] {
			return nil, fmt.Errorf("store: rule %d invalid action %q", i, r.Action)
		}
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM traffic_rules WHERE project_id = ?`, projectID); err != nil {
		return nil, err
	}
	ts := nowString()
	out := make([]model.TrafficRule, 0, len(rules))
	for i, r := range rules {
		if r.ID == "" {
			r.ID = uuid.NewString()
		}
		r.ProjectID = projectID
		r.Seq = i
		params, _ := json.Marshal(r.Params)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO traffic_rules (id, project_id, seq, enabled, phase, match_expr, action, params, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			r.ID, projectID, i, r.Enabled, r.Phase, r.Match, r.Action, string(params), ts); err != nil {
			return nil, err
		}
		r.CreatedAt = parseTime(ts)
		out = append(out, r)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
