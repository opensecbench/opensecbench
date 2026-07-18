package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// CreateReport records a generated report (its bytes must already be in the CAS as artifactID).
func (db *DB) CreateReport(ctx context.Context, r model.Report) (model.Report, error) {
	if r.ProjectID == "" || r.ArtifactID == "" {
		return model.Report{}, errors.New("store: report project id and artifact id required")
	}
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO reports (id, project_id, template_id, format, title, artifact_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ProjectID, r.TemplateID, r.Format, r.Title, r.ArtifactID, ts); err != nil {
		return model.Report{}, err
	}
	r.CreatedAt = parseTime(ts)
	return r, nil
}

// GetReport returns a report by id.
func (db *DB) GetReport(ctx context.Context, id string) (model.Report, error) {
	var r model.Report
	var created string
	err := db.QueryRowContext(ctx,
		`SELECT id, project_id, template_id, format, title, artifact_id, created_at FROM reports WHERE id = ?`, id).
		Scan(&r.ID, &r.ProjectID, &r.TemplateID, &r.Format, &r.Title, &r.ArtifactID, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Report{}, ErrNotFound
	}
	if err != nil {
		return model.Report{}, err
	}
	r.CreatedAt = parseTime(created)
	return r, nil
}

// ListReportsByProject returns a project's generated reports, newest first.
func (db *DB) ListReportsByProject(ctx context.Context, projectID string) ([]model.Report, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, template_id, format, title, artifact_id, created_at
		 FROM reports WHERE project_id = ? ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.Report
	for rows.Next() {
		var r model.Report
		var created string
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.TemplateID, &r.Format, &r.Title, &r.ArtifactID, &created); err != nil {
			return nil, err
		}
		r.CreatedAt = parseTime(created)
		out = append(out, r)
	}
	return out, rows.Err()
}
