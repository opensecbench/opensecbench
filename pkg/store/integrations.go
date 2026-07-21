package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// SetBinding upserts a project's binding to a global connector (ADR-0027 / IA declutter) — which connector
// this project uses and its project-side scope (project_key).
func (db *DB) SetBinding(ctx context.Context, b model.IntegrationBinding) (model.IntegrationBinding, error) {
	if b.ProjectID == "" || b.ConnectorID == "" {
		return model.IntegrationBinding{}, errors.New("store: binding needs a project id and connector id")
	}
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	ts := nowString()
	_, err := db.ExecContext(ctx,
		`INSERT INTO integration_bindings (id, project_id, connector_id, project_key, created_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(project_id, connector_id) DO UPDATE SET project_key = excluded.project_key`,
		b.ID, b.ProjectID, b.ConnectorID, b.ProjectKey, ts)
	if err != nil {
		return model.IntegrationBinding{}, err
	}
	return db.GetBinding(ctx, b.ProjectID, b.ConnectorID)
}

const bindingCols = `id, project_id, connector_id, project_key, created_at`

func scanBinding(s interface{ Scan(...any) error }) (model.IntegrationBinding, error) {
	var b model.IntegrationBinding
	var created string
	if err := s.Scan(&b.ID, &b.ProjectID, &b.ConnectorID, &b.ProjectKey, &created); err != nil {
		return model.IntegrationBinding{}, err
	}
	b.CreatedAt = parseTime(created)
	return b, nil
}

// GetBinding returns a project's binding to one connector.
func (db *DB) GetBinding(ctx context.Context, projectID, connectorID string) (model.IntegrationBinding, error) {
	b, err := scanBinding(db.QueryRowContext(ctx,
		`SELECT `+bindingCols+` FROM integration_bindings WHERE project_id = ? AND connector_id = ?`, projectID, connectorID))
	if errors.Is(err, sql.ErrNoRows) {
		return model.IntegrationBinding{}, ErrNotFound
	}
	return b, err
}

// ListBindings returns a project's connector bindings.
func (db *DB) ListBindings(ctx context.Context, projectID string) ([]model.IntegrationBinding, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+bindingCols+` FROM integration_bindings WHERE project_id = ? ORDER BY created_at`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.IntegrationBinding
	for rows.Next() {
		b, err := scanBinding(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

// DeleteBinding removes a project's binding to a connector.
func (db *DB) DeleteBinding(ctx context.Context, projectID, connectorID string) error {
	res, err := db.ExecContext(ctx,
		`DELETE FROM integration_bindings WHERE project_id = ? AND connector_id = ?`, projectID, connectorID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// HasImport reports whether an external finding has already been imported (inbound idempotency), keyed by
// connector.
func (db *DB) HasImport(ctx context.Context, projectID, connectorID, externalID string) (bool, error) {
	var one int
	err := db.QueryRowContext(ctx,
		`SELECT 1 FROM integration_imports WHERE project_id = ? AND connector_id = ? AND external_id = ?`,
		projectID, connectorID, externalID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// RecordImport marks an external finding imported to an observation (idempotent per external id).
func (db *DB) RecordImport(ctx context.Context, projectID, connectorID, externalID, observationID string) error {
	_, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO integration_imports (id, project_id, connector_id, external_id, observation_id, imported_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), projectID, connectorID, externalID, observationID, time.Now().UTC().Format(timeLayout))
	return err
}
