package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// CreatePlan inserts a plan and its steps (status defaults: plan running, steps pending). Step ids and
// seq are assigned here; DependsOn is stored as a comma-separated key list.
func (db *DB) CreatePlan(ctx context.Context, p model.Plan) (model.Plan, error) {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return model.Plan{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if p.ID == "" {
		p.ID = uuid.NewString()
	}
	if p.Status == "" {
		p.Status = model.PlanRunning
	}
	ts := nowString()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO plans (id, project_id, playbook_id, goal, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.ProjectID, p.PlaybookID, p.Goal, p.Status, ts, ts); err != nil {
		return model.Plan{}, err
	}
	for i := range p.Steps {
		s := &p.Steps[i]
		s.ID = uuid.NewString()
		s.PlanID = p.ID
		s.Seq = i
		if s.Status == "" {
			s.Status = model.StepPending
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO plan_steps (id, plan_id, seq, step_key, profile, instruction, depends_on, gate, status, result, error)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, '', '')`,
			s.ID, p.ID, i, s.Key, s.Profile, s.Instruction, strings.Join(s.DependsOn, ","), boolToInt(s.Gate), s.Status); err != nil {
			return model.Plan{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return model.Plan{}, err
	}
	p.CreatedAt, p.UpdatedAt = parseTime(ts), parseTime(ts)
	return p, nil
}

// UpdatePlanStep sets a step's status, result, and error.
func (db *DB) UpdatePlanStep(ctx context.Context, stepID, status, result, errMsg string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE plan_steps SET status = ?, result = ?, error = ? WHERE id = ?`, status, result, errMsg, stepID)
	return err
}

// AppendPlanStepProgress appends a line to a step's live activity trail (best-effort streaming; the
// caller ignores errors so a progress write never derails the run).
func (db *DB) AppendPlanStepProgress(ctx context.Context, stepID, line string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE plan_steps SET progress = progress || ? WHERE id = ?`, line, stepID)
	return err
}

// UpdatePlanStatus sets a plan's status.
func (db *DB) UpdatePlanStatus(ctx context.Context, planID, status string) error {
	_, err := db.ExecContext(ctx,
		`UPDATE plans SET status = ?, updated_at = ? WHERE id = ?`, status, nowString(), planID)
	return err
}

// GetPlan returns a plan with its steps in order.
func (db *DB) GetPlan(ctx context.Context, id string) (model.Plan, error) {
	var p model.Plan
	var created, updated string
	err := db.QueryRowContext(ctx,
		`SELECT id, project_id, playbook_id, goal, status, created_at, updated_at FROM plans WHERE id = ?`, id).
		Scan(&p.ID, &p.ProjectID, &p.PlaybookID, &p.Goal, &p.Status, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Plan{}, ErrNotFound
	}
	if err != nil {
		return model.Plan{}, err
	}
	p.CreatedAt, p.UpdatedAt = parseTime(created), parseTime(updated)
	steps, err := db.listPlanSteps(ctx, id)
	if err != nil {
		return model.Plan{}, err
	}
	p.Steps = steps
	return p, nil
}

func (db *DB) listPlanSteps(ctx context.Context, planID string) ([]model.PlanStep, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, plan_id, seq, step_key, profile, instruction, depends_on, gate, gate_approved, status, result, error, progress
		 FROM plan_steps WHERE plan_id = ? ORDER BY seq`, planID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.PlanStep
	for rows.Next() {
		var s model.PlanStep
		var deps string
		var gate, gateApproved int
		if err := rows.Scan(&s.ID, &s.PlanID, &s.Seq, &s.Key, &s.Profile, &s.Instruction, &deps, &gate, &gateApproved, &s.Status, &s.Result, &s.Error, &s.Progress); err != nil {
			return nil, err
		}
		if deps != "" {
			s.DependsOn = strings.Split(deps, ",")
		}
		s.Gate, s.GateApproved = gate != 0, gateApproved != 0
		out = append(out, s)
	}
	return out, rows.Err()
}

// ResolvePlanGate records a human's decision on a waiting gate step (ADR-0044). Approve clears the gate
// (status back to pending + gate_approved=1) so a resumed run executes the step; deny marks it skipped so
// the run propagates the skip to the gate's dependents and ends. Returns ErrNotFound if the step isn't a
// gate currently waiting.
func (db *DB) ResolvePlanGate(ctx context.Context, stepID string, approve bool, note string) error {
	status, approved := model.StepSkipped, 0
	if approve {
		status, approved = model.StepPending, 1
	}
	res, err := db.ExecContext(ctx,
		`UPDATE plan_steps SET status = ?, gate_approved = ?, result = ?, error = ?
		 WHERE id = ? AND gate = 1 AND status = ?`,
		status, approved, gateResult(approve, note), gateError(approve, note), stepID, model.StepWaiting)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

func gateResult(approve bool, note string) string {
	if !approve {
		return ""
	}
	if note == "" {
		return "approved"
	}
	return "approved: " + note
}

func gateError(approve bool, note string) string {
	if approve {
		return ""
	}
	if note == "" {
		return "denied by human"
	}
	return "denied: " + note
}

// ListPlansByProject returns a project's plans (without steps), newest first.
// ListRunningPlans returns plans still in flight (running or waiting on a gate) in this database.
func (db *DB) ListRunningPlans(ctx context.Context) ([]model.Plan, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, playbook_id, goal, status, created_at, updated_at
		 FROM plans WHERE status IN ('running', 'waiting') ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Plan
	for rows.Next() {
		var p model.Plan
		var created, updated string
		if err := rows.Scan(&p.ID, &p.ProjectID, &p.PlaybookID, &p.Goal, &p.Status, &created, &updated); err != nil {
			return nil, err
		}
		p.CreatedAt, p.UpdatedAt = parseTime(created), parseTime(updated)
		out = append(out, p)
	}
	return out, rows.Err()
}

func (db *DB) ListPlansByProject(ctx context.Context, projectID string) ([]model.Plan, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, playbook_id, goal, status, created_at, updated_at
		 FROM plans WHERE project_id = ? ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Plan
	for rows.Next() {
		var p model.Plan
		var created, updated string
		if err := rows.Scan(&p.ID, &p.ProjectID, &p.PlaybookID, &p.Goal, &p.Status, &created, &updated); err != nil {
			return nil, err
		}
		p.CreatedAt, p.UpdatedAt = parseTime(created), parseTime(updated)
		out = append(out, p)
	}
	return out, rows.Err()
}
