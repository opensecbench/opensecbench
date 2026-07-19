package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// CreateInvestigation opens an investigation for an observation (ADR-0028). One per observation — a repeat
// returns the existing one (idempotent, so a re-run of the same disposition doesn't duplicate).
func (db *DB) CreateInvestigation(ctx context.Context, inv model.Investigation) (model.Investigation, error) {
	if inv.ProjectID == "" || inv.ObservationID == "" {
		return model.Investigation{}, errors.New("store: investigation needs project id and observation id")
	}
	if existing, err := db.investigationByObservation(ctx, inv.ObservationID); err == nil {
		return existing, nil
	}
	inv.ID = uuid.NewString()
	now := time.Now().UTC()
	inv.CreatedAt, inv.UpdatedAt = now, now
	if inv.Status == "" {
		inv.Status = model.InvestigationOpen
	}
	nowStr := now.Format(timeLayout)
	_, err := db.ExecContext(ctx,
		`INSERT INTO investigations (id, project_id, application_id, observation_id, title, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		inv.ID, inv.ProjectID, inv.ApplicationID, inv.ObservationID, inv.Title, inv.Status, nowStr, nowStr)
	if err != nil {
		return model.Investigation{}, err
	}
	return inv, nil
}

const investigationCols = `id, project_id, application_id, observation_id, title, status, thread_id, created_at, updated_at`

func scanInvestigation(s interface{ Scan(...any) error }) (model.Investigation, error) {
	var inv model.Investigation
	var app, thread sql.NullString
	var created, updated string
	if err := s.Scan(&inv.ID, &inv.ProjectID, &app, &inv.ObservationID, &inv.Title, &inv.Status, &thread, &created, &updated); err != nil {
		return model.Investigation{}, err
	}
	inv.ApplicationID, inv.ThreadID = ptr(app), ptr(thread)
	inv.CreatedAt = parseTime(created)
	inv.UpdatedAt = parseTime(updated)
	return inv, nil
}

func (db *DB) investigationByObservation(ctx context.Context, observationID string) (model.Investigation, error) {
	inv, err := scanInvestigation(db.QueryRowContext(ctx,
		`SELECT `+investigationCols+` FROM investigations WHERE observation_id = ?`, observationID))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Investigation{}, ErrNotFound
	}
	return inv, err
}

// GetInvestigation returns one investigation by id.
func (db *DB) GetInvestigation(ctx context.Context, id string) (model.Investigation, error) {
	inv, err := scanInvestigation(db.QueryRowContext(ctx,
		`SELECT `+investigationCols+` FROM investigations WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Investigation{}, ErrNotFound
	}
	return inv, err
}

// ListInvestigationsByProject returns a project's investigations, newest first.
func (db *DB) ListInvestigationsByProject(ctx context.Context, projectID string) ([]model.Investigation, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+investigationCols+` FROM investigations WHERE project_id = ? ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Investigation
	for rows.Next() {
		inv, err := scanInvestigation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, inv)
	}
	return out, rows.Err()
}

// SetInvestigationStatus updates an investigation's status.
func (db *DB) SetInvestigationStatus(ctx context.Context, id, status string) error {
	res, err := db.ExecContext(ctx,
		`UPDATE investigations SET status = ?, updated_at = ? WHERE id = ?`,
		status, time.Now().UTC().Format(timeLayout), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// SetInvestigationThread links an investigation to its agent thread and marks it investigating.
func (db *DB) SetInvestigationThread(ctx context.Context, id, threadID string) error {
	res, err := db.ExecContext(ctx,
		`UPDATE investigations SET thread_id = ?, status = ?, updated_at = ? WHERE id = ?`,
		threadID, model.InvestigationInvestigating, time.Now().UTC().Format(timeLayout), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// InvestigationForVuln returns the investigation already tracking any of these advisory ids in the project,
// if one exists (ADR-0037). Used to avoid opening a second investigation when another tool reports the same
// vulnerability under a different id scheme (grype GHSA vs govulncheck CVE).
func (db *DB) InvestigationForVuln(ctx context.Context, projectID string, vulnIDs []string) (string, bool) {
	if projectID == "" || len(vulnIDs) == 0 {
		return "", false
	}
	q := `SELECT investigation_id FROM investigation_vulns WHERE project_id = ? AND vuln_id IN (?` +
		strings.Repeat(", ?", len(vulnIDs)-1) + `) LIMIT 1`
	args := make([]any, 0, len(vulnIDs)+1)
	args = append(args, projectID)
	for _, v := range vulnIDs {
		args = append(args, v)
	}
	var id string
	if err := db.QueryRowContext(ctx, q, args...).Scan(&id); err != nil {
		return "", false
	}
	return id, true
}

// RecordInvestigationVulns claims each advisory id for an investigation (ADR-0037). Idempotent: an id
// already claimed (by this or another investigation) is left as-is, so the first investigation owns the vuln.
func (db *DB) RecordInvestigationVulns(ctx context.Context, projectID, investigationID string, vulnIDs []string) error {
	now := time.Now().UTC().Format(timeLayout)
	for _, v := range vulnIDs {
		if v == "" {
			continue
		}
		if _, err := db.ExecContext(ctx,
			`INSERT OR IGNORE INTO investigation_vulns (id, investigation_id, project_id, vuln_id, created_at)
			 VALUES (?, ?, ?, ?, ?)`,
			uuid.NewString(), investigationID, projectID, v, now); err != nil {
			return err
		}
	}
	return nil
}
