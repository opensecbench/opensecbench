package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// SetIntegrationConfig upserts a project's config for an integration (ADR-0027). credential is a vault
// secret name, never a value.
func (db *DB) SetIntegrationConfig(ctx context.Context, c model.IntegrationConfig) (model.IntegrationConfig, error) {
	if c.ProjectID == "" || c.Integration == "" {
		return model.IntegrationConfig{}, errors.New("store: integration config needs project id and integration")
	}
	now := time.Now().UTC()
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	c.CreatedAt, c.UpdatedAt = now, now
	nowStr := now.Format(timeLayout)
	_, err := db.ExecContext(ctx,
		`INSERT INTO integration_configs (id, project_id, integration, base_url, project_key, credential, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(project_id, integration) DO UPDATE SET
		   base_url = excluded.base_url, project_key = excluded.project_key,
		   credential = excluded.credential, updated_at = excluded.updated_at`,
		c.ID, c.ProjectID, c.Integration, c.BaseURL, c.ProjectKey, c.Credential, nowStr, nowStr)
	if err != nil {
		return model.IntegrationConfig{}, err
	}
	return db.GetIntegrationConfig(ctx, c.ProjectID, c.Integration)
}

const integrationCols = `id, project_id, integration, base_url, project_key, credential, created_at, updated_at`

func scanIntegrationConfig(s interface{ Scan(...any) error }) (model.IntegrationConfig, error) {
	var c model.IntegrationConfig
	var created, updated string
	if err := s.Scan(&c.ID, &c.ProjectID, &c.Integration, &c.BaseURL, &c.ProjectKey, &c.Credential, &created, &updated); err != nil {
		return model.IntegrationConfig{}, err
	}
	c.CreatedAt = parseTime(created)
	c.UpdatedAt = parseTime(updated)
	return c, nil
}

// GetIntegrationConfig returns a project's config for one integration.
func (db *DB) GetIntegrationConfig(ctx context.Context, projectID, integration string) (model.IntegrationConfig, error) {
	c, err := scanIntegrationConfig(db.QueryRowContext(ctx,
		`SELECT `+integrationCols+` FROM integration_configs WHERE project_id = ? AND integration = ?`, projectID, integration))
	if errors.Is(err, sql.ErrNoRows) {
		return model.IntegrationConfig{}, ErrNotFound
	}
	return c, err
}

// ListIntegrationConfigs returns a project's integration configs.
func (db *DB) ListIntegrationConfigs(ctx context.Context, projectID string) ([]model.IntegrationConfig, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+integrationCols+` FROM integration_configs WHERE project_id = ? ORDER BY integration`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.IntegrationConfig
	for rows.Next() {
		c, err := scanIntegrationConfig(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// DeleteIntegrationConfig removes a project's config for an integration.
func (db *DB) DeleteIntegrationConfig(ctx context.Context, projectID, integration string) error {
	res, err := db.ExecContext(ctx,
		`DELETE FROM integration_configs WHERE project_id = ? AND integration = ?`, projectID, integration)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// HasImport reports whether an external finding has already been imported (inbound idempotency).
func (db *DB) HasImport(ctx context.Context, projectID, integration, externalID string) (bool, error) {
	var one int
	err := db.QueryRowContext(ctx,
		`SELECT 1 FROM integration_imports WHERE project_id = ? AND integration = ? AND external_id = ?`,
		projectID, integration, externalID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// RecordImport marks an external finding imported to an observation (idempotent per external id).
func (db *DB) RecordImport(ctx context.Context, projectID, integration, externalID, observationID string) error {
	_, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO integration_imports (id, project_id, integration, external_id, observation_id, imported_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		uuid.NewString(), projectID, integration, externalID, observationID, time.Now().UTC().Format(timeLayout))
	return err
}
