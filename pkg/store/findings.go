package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// --- Observations ---

// CreateObservation inserts an observation. Interpreters and the engine use this to record
// tool/AI-derived results as unreviewed evidence (ADR-0005).
func (db *DB) CreateObservation(ctx context.Context, o model.Observation) (model.Observation, error) {
	if o.Title == "" {
		return model.Observation{}, errors.New("store: observation title required")
	}
	if o.ID == "" {
		o.ID = uuid.NewString()
	}
	if o.Origin == "" {
		o.Origin = model.OriginTool
	}
	if o.ReviewState == "" {
		o.ReviewState = model.ReviewUnreviewed
	}
	if o.Severity == "" {
		o.Severity = "info"
	}
	o.CreatedAt = time.Now().UTC()
	_, err := db.ExecContext(ctx,
		`INSERT INTO observations
		 (id, task_id, artifact_id, origin, review_state, title, detail, severity, rule_id, location, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		o.ID, o.TaskID, o.ArtifactID, o.Origin, o.ReviewState, o.Title, o.Detail, o.Severity,
		o.RuleID, o.Location, o.CreatedAt.Format(timeLayout))
	if err != nil {
		return model.Observation{}, err
	}
	return o, nil
}

// ListObservationsByTask returns a task's observations, oldest first.
func (db *DB) ListObservationsByTask(ctx context.Context, taskID string) ([]model.Observation, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, task_id, artifact_id, origin, review_state, title, detail, severity, rule_id, location, created_at
		 FROM observations WHERE task_id = ? ORDER BY created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanObservations(rows)
}

// GetObservation returns one observation by id.
func (db *DB) GetObservation(ctx context.Context, id string) (model.Observation, error) {
	var o model.Observation
	var task, artifact sql.NullString
	var created string
	err := db.QueryRowContext(ctx,
		`SELECT id, task_id, artifact_id, origin, review_state, title, detail, severity, rule_id, location, created_at
		 FROM observations WHERE id = ?`, id).
		Scan(&o.ID, &task, &artifact, &o.Origin, &o.ReviewState, &o.Title, &o.Detail,
			&o.Severity, &o.RuleID, &o.Location, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Observation{}, ErrNotFound
	}
	if err != nil {
		return model.Observation{}, err
	}
	o.TaskID, o.ArtifactID = ptr(task), ptr(artifact)
	o.CreatedAt = parseTime(created)
	return o, nil
}

func scanObservations(rows *sql.Rows) ([]model.Observation, error) {
	var out []model.Observation
	for rows.Next() {
		var o model.Observation
		var task, artifact sql.NullString
		var created string
		if err := rows.Scan(&o.ID, &task, &artifact, &o.Origin, &o.ReviewState, &o.Title, &o.Detail,
			&o.Severity, &o.RuleID, &o.Location, &created); err != nil {
			return nil, err
		}
		o.TaskID, o.ArtifactID = ptr(task), ptr(artifact)
		o.CreatedAt = parseTime(created)
		out = append(out, o)
	}
	return out, rows.Err()
}

// ReviewObservation sets an observation's review state (confirmed | rejected | unreviewed).
func (db *DB) ReviewObservation(ctx context.Context, id, state string) error {
	switch state {
	case model.ReviewConfirmed, model.ReviewRejected, model.ReviewUnreviewed:
	default:
		return fmt.Errorf("store: invalid review state %q", state)
	}
	res, err := db.ExecContext(ctx, `UPDATE observations SET review_state = ? WHERE id = ?`, state, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Findings ---

// NewFinding is the input for creating a finding from confirmed observations.
type NewFinding struct {
	ApplicationID  *string
	Title          string
	Severity       string
	Description    string
	CWE            string
	ObservationIDs []string
}

// CreateFinding assembles a finding from observations. Per ADR-0005, every supporting observation
// must already be confirmed; otherwise the finding is rejected.
func (db *DB) CreateFinding(ctx context.Context, nf NewFinding) (model.Finding, error) {
	if nf.Title == "" {
		return model.Finding{}, errors.New("store: finding title required")
	}
	severity := nf.Severity
	if severity == "" {
		severity = "medium"
	}
	now := time.Now().UTC()
	nowStr := now.Format(timeLayout)
	f := model.Finding{
		ID:             uuid.NewString(),
		ApplicationID:  nf.ApplicationID,
		Title:          nf.Title,
		Severity:       severity,
		Status:         model.FindingOpen,
		Description:    nf.Description,
		CWE:            nf.CWE,
		ObservationIDs: nf.ObservationIDs,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return model.Finding{}, err
	}
	defer func() { _ = tx.Rollback() }()

	for _, oid := range nf.ObservationIDs {
		var state string
		err := tx.QueryRowContext(ctx, `SELECT review_state FROM observations WHERE id = ?`, oid).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return model.Finding{}, fmt.Errorf("store: observation %s not found", oid)
		}
		if err != nil {
			return model.Finding{}, err
		}
		if state != model.ReviewConfirmed {
			return model.Finding{}, fmt.Errorf("store: observation %s is %s, must be confirmed to support a finding", oid, state)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO findings (id, application_id, title, severity, status, description, cwe, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, nf.ApplicationID, f.Title, f.Severity, f.Status, f.Description, f.CWE, nowStr, nowStr); err != nil {
		return model.Finding{}, err
	}
	for _, oid := range nf.ObservationIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO finding_observations (finding_id, observation_id) VALUES (?, ?)`, f.ID, oid); err != nil {
			return model.Finding{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return model.Finding{}, err
	}
	return f, nil
}

// GetFinding returns a finding with its supporting observation ids.
func (db *DB) GetFinding(ctx context.Context, id string) (model.Finding, error) {
	var f model.Finding
	var app sql.NullString
	var created, updated string
	err := db.QueryRowContext(ctx,
		`SELECT id, application_id, title, severity, status, description, cwe, created_at, updated_at
		 FROM findings WHERE id = ?`, id).
		Scan(&f.ID, &app, &f.Title, &f.Severity, &f.Status, &f.Description, &f.CWE, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Finding{}, ErrNotFound
	}
	if err != nil {
		return model.Finding{}, err
	}
	f.ApplicationID = ptr(app)
	f.CreatedAt, f.UpdatedAt = parseTime(created), parseTime(updated)

	rows, err := db.QueryContext(ctx,
		`SELECT observation_id FROM finding_observations WHERE finding_id = ? ORDER BY observation_id`, id)
	if err != nil {
		return model.Finding{}, err
	}
	defer func() { _ = rows.Close() }()
	f.ObservationIDs = []string{}
	for rows.Next() {
		var oid string
		if err := rows.Scan(&oid); err != nil {
			return model.Finding{}, err
		}
		f.ObservationIDs = append(f.ObservationIDs, oid)
	}
	return f, rows.Err()
}

// ListFindings returns all findings, newest first (without supporting-observation ids).
func (db *DB) ListFindings(ctx context.Context) ([]model.Finding, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, application_id, title, severity, status, description, cwe, created_at, updated_at
		 FROM findings ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.Finding
	for rows.Next() {
		var f model.Finding
		var app sql.NullString
		var created, updated string
		if err := rows.Scan(&f.ID, &app, &f.Title, &f.Severity, &f.Status, &f.Description, &f.CWE, &created, &updated); err != nil {
			return nil, err
		}
		f.ApplicationID = ptr(app)
		f.CreatedAt, f.UpdatedAt = parseTime(created), parseTime(updated)
		out = append(out, f)
	}
	return out, rows.Err()
}
