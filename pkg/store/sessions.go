package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// CreateSession records a new active interactive session.
func (db *DB) CreateSession(ctx context.Context, s model.Session) (model.Session, error) {
	if s.ProjectID == "" {
		return model.Session{}, errors.New("store: session project id required")
	}
	if s.ID == "" {
		s.ID = uuid.NewString()
	}
	if s.Kind == "" {
		s.Kind = model.SessionTerminal
	}
	s.Status = model.SessionActive
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO sessions (id, project_id, kind, runner, container, image, status, actor, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		s.ID, s.ProjectID, s.Kind, s.Runner, s.Container, s.Image, s.Status, s.Actor, ts); err != nil {
		return model.Session{}, err
	}
	s.CreatedAt = parseTime(ts)
	return s, nil
}

// CloseSession marks a session closed (or errored), attaching the transcript artifact if captured.
func (db *DB) CloseSession(ctx context.Context, id, status string, transcriptArtifactID *string, errMsg string) error {
	if status == "" {
		status = model.SessionClosed
	}
	res, err := db.ExecContext(ctx,
		`UPDATE sessions SET status = ?, transcript_artifact_id = ?, error = ?, closed_at = ?
		 WHERE id = ? AND status = ?`,
		status, transcriptArtifactID, errMsg, nowString(), id, model.SessionActive)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const sessionCols = `id, project_id, kind, runner, container, image, status, actor,
	transcript_artifact_id, error, created_at, closed_at`

func scanSession(s interface{ Scan(...any) error }) (model.Session, error) {
	var m model.Session
	var transcript, closed sql.NullString
	var created string
	if err := s.Scan(&m.ID, &m.ProjectID, &m.Kind, &m.Runner, &m.Container, &m.Image, &m.Status,
		&m.Actor, &transcript, &m.Error, &created, &closed); err != nil {
		return model.Session{}, err
	}
	m.TranscriptArtifactID = ptr(transcript)
	m.CreatedAt = parseTime(created)
	m.ClosedAt = ptrTime(closed)
	return m, nil
}

// GetSession returns a session by id.
func (db *DB) GetSession(ctx context.Context, id string) (model.Session, error) {
	row := db.QueryRowContext(ctx, `SELECT `+sessionCols+` FROM sessions WHERE id = ?`, id)
	m, err := scanSession(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Session{}, ErrNotFound
	}
	return m, err
}

// ListSessionsByProject returns a project's sessions, newest first.
func (db *DB) ListSessionsByProject(ctx context.Context, projectID string) ([]model.Session, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+sessionCols+` FROM sessions WHERE project_id = ? ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.Session
	for rows.Next() {
		m, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}
