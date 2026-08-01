package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/action"
)

// Action runs (ADR-0059) are per-project: they act on a project-local finding/observation and their
// output artifact lives in the project CAS. The definition is global (see actions.go).

// CreateActionRun records a newly started run (status "running").
func (db *DB) CreateActionRun(ctx context.Context, r action.Run) (action.Run, error) {
	if r.ActionID == "" || r.SubjectID == "" || r.SubjectKind == "" {
		return action.Run{}, errors.New("store: action run needs an action, subject kind, and subject id")
	}
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.Status == "" {
		r.Status = action.RunRunning
	}
	ts := nowString()
	r.CreatedAt = parseTime(ts)
	if _, err := db.ExecContext(ctx,
		`INSERT INTO action_runs (id, action_id, action_name, kind, subject_kind, subject_id, status,
			summary, output, artifact_id, error, created_at, finished_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '')`,
		r.ID, r.ActionID, r.ActionName, string(r.Kind), r.SubjectKind, r.SubjectID, r.Status,
		r.Summary, r.Output, r.ArtifactID, r.Error, ts); err != nil {
		return action.Run{}, err
	}
	return r, nil
}

// FinishActionRun records a run's terminal state (done/error) with its output and artifact.
func (db *DB) FinishActionRun(ctx context.Context, id, status, summary, output, artifactID, runErr string) error {
	ts := nowString()
	res, err := db.ExecContext(ctx,
		`UPDATE action_runs SET status=?, summary=?, output=?, artifact_id=?, error=?, finished_at=? WHERE id=?`,
		status, summary, output, artifactID, runErr, ts, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetActionRun returns one run by id.
func (db *DB) GetActionRun(ctx context.Context, id string) (action.Run, error) {
	row := db.QueryRowContext(ctx, actionRunSelect+` WHERE id = ?`, id)
	r, err := scanActionRun(row)
	if errors.Is(err, sql.ErrNoRows) {
		return action.Run{}, ErrNotFound
	}
	return r, err
}

// ListActionRunsBySubject returns a subject's runs, newest first.
func (db *DB) ListActionRunsBySubject(ctx context.Context, subjectKind, subjectID string) ([]action.Run, error) {
	rows, err := db.QueryContext(ctx,
		actionRunSelect+` WHERE subject_kind = ? AND subject_id = ? ORDER BY created_at DESC`, subjectKind, subjectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []action.Run
	for rows.Next() {
		r, err := scanActionRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

const actionRunSelect = `SELECT id, action_id, action_name, kind, subject_kind, subject_id, status,
	summary, output, artifact_id, error, created_at, finished_at FROM action_runs`

func scanActionRun(row scanner) (action.Run, error) {
	var r action.Run
	var kind, created, finished string
	if err := row.Scan(&r.ID, &r.ActionID, &r.ActionName, &kind, &r.SubjectKind, &r.SubjectID, &r.Status,
		&r.Summary, &r.Output, &r.ArtifactID, &r.Error, &created, &finished); err != nil {
		return action.Run{}, err
	}
	r.Kind = action.Kind(kind)
	r.CreatedAt = parseTime(created)
	if finished != "" {
		t := parseTime(finished)
		r.FinishedAt = &t
	}
	return r, nil
}
