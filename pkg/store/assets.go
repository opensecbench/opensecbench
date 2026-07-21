package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// --- Applications ---

// CreateApplication inserts an application under a project.
func (db *DB) CreateApplication(ctx context.Context, projectID, name string) (model.Application, error) {
	if projectID == "" || name == "" {
		return model.Application{}, errors.New("store: application project id and name required")
	}
	a := model.Application{ID: uuid.NewString(), ProjectID: projectID, Name: name}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO applications (id, project_id, name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
		a.ID, projectID, name, ts, ts); err != nil {
		return model.Application{}, err
	}
	a.CreatedAt, a.UpdatedAt = parseTime(ts), parseTime(ts)
	return a, nil
}

// ListApplicationsByProject returns a project's applications ordered by name.
func (db *DB) ListApplicationsByProject(ctx context.Context, projectID string) ([]model.Application, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, name, created_at, updated_at FROM applications WHERE project_id = ? ORDER BY name`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.Application
	for rows.Next() {
		var a model.Application
		var created, updated string
		if err := rows.Scan(&a.ID, &a.ProjectID, &a.Name, &created, &updated); err != nil {
			return nil, err
		}
		a.CreatedAt, a.UpdatedAt = parseTime(created), parseTime(updated)
		out = append(out, a)
	}
	return out, rows.Err()
}

// GetApplication returns an application by id.
func (db *DB) GetApplication(ctx context.Context, id string) (model.Application, error) {
	var a model.Application
	var created, updated string
	err := db.QueryRowContext(ctx,
		`SELECT id, project_id, name, created_at, updated_at FROM applications WHERE id = ?`, id).
		Scan(&a.ID, &a.ProjectID, &a.Name, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Application{}, ErrNotFound
	}
	if err != nil {
		return model.Application{}, err
	}
	a.CreatedAt, a.UpdatedAt = parseTime(created), parseTime(updated)
	return a, nil
}

// --- Assets ---

var validAssetTypes = map[string]bool{
	model.AssetSourceRepo:      true,
	model.AssetCloudDeployment: true,
	model.AssetInfrastructure:  true,
	model.AssetDocument:        true,
	model.AssetCorrespondence:  true,
}

// NewAsset is the input for creating an asset. An empty Sensitivity is inferred from Location.
type NewAsset struct {
	ApplicationID string
	Type          string
	Location      string
	Sensitivity   string
}

// CreateAsset inserts an asset, defaulting its sensitivity from the location when unset.
func (db *DB) CreateAsset(ctx context.Context, na NewAsset) (model.Asset, error) {
	if na.ApplicationID == "" || na.Location == "" {
		return model.Asset{}, errors.New("store: asset application id and location required")
	}
	if !validAssetTypes[na.Type] {
		return model.Asset{}, fmt.Errorf("store: invalid asset type %q", na.Type)
	}
	sensitivity := na.Sensitivity
	if sensitivity == "" {
		sensitivity = model.InferSensitivity(na.Location)
	}

	a := model.Asset{
		ID:            uuid.NewString(),
		ApplicationID: na.ApplicationID,
		Type:          na.Type,
		Location:      na.Location,
		Sensitivity:   sensitivity,
	}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO assets (id, application_id, type, location, sensitivity, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.ApplicationID, a.Type, a.Location, a.Sensitivity, ts, ts); err != nil {
		return model.Asset{}, err
	}
	a.CreatedAt, a.UpdatedAt = parseTime(ts), parseTime(ts)
	return a, nil
}

var validSensitivities = map[string]bool{
	model.SensitivityPrivate:    true,
	model.SensitivityInternal:   true,
	model.SensitivityOpenSource: true,
}

// UpdateAssetSensitivity changes an asset's sensitivity in place and returns the updated asset. The
// sensitivity must be one of the known values — unlike create, an empty value is not inferred here,
// because the caller is editing an existing asset deliberately.
func (db *DB) UpdateAssetSensitivity(ctx context.Context, id, sensitivity string) (model.Asset, error) {
	if !validSensitivities[sensitivity] {
		return model.Asset{}, fmt.Errorf("store: invalid sensitivity %q", sensitivity)
	}
	res, err := db.ExecContext(ctx,
		`UPDATE assets SET sensitivity = ?, updated_at = ? WHERE id = ?`,
		sensitivity, nowString(), id)
	if err != nil {
		return model.Asset{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.Asset{}, ErrNotFound
	}
	return db.GetAsset(ctx, id)
}

// ListAssetsByApplication returns an application's assets, oldest first.
func (db *DB) ListAssetsByApplication(ctx context.Context, applicationID string) ([]model.Asset, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, application_id, type, location, sensitivity, created_at, updated_at
		 FROM assets WHERE application_id = ? ORDER BY created_at`, applicationID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanAssets(rows)
}

// ListAssets returns all assets, newest first (used by the Analyst to find scan targets).
func (db *DB) ListAssets(ctx context.Context) ([]model.Asset, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, application_id, type, location, sensitivity, created_at, updated_at
		 FROM assets ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanAssets(rows)
}

// GetAsset returns an asset by id.
func (db *DB) GetAsset(ctx context.Context, id string) (model.Asset, error) {
	var a model.Asset
	var created, updated string
	err := db.QueryRowContext(ctx,
		`SELECT id, application_id, type, location, sensitivity, created_at, updated_at FROM assets WHERE id = ?`, id).
		Scan(&a.ID, &a.ApplicationID, &a.Type, &a.Location, &a.Sensitivity, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Asset{}, ErrNotFound
	}
	if err != nil {
		return model.Asset{}, err
	}
	a.CreatedAt, a.UpdatedAt = parseTime(created), parseTime(updated)
	return a, nil
}

func scanAssets(rows *sql.Rows) ([]model.Asset, error) {
	var out []model.Asset
	for rows.Next() {
		var a model.Asset
		var created, updated string
		if err := rows.Scan(&a.ID, &a.ApplicationID, &a.Type, &a.Location, &a.Sensitivity, &created, &updated); err != nil {
			return nil, err
		}
		a.CreatedAt, a.UpdatedAt = parseTime(created), parseTime(updated)
		out = append(out, a)
	}
	return out, rows.Err()
}
