package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

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
	model.AssetWebService:      true,
	model.AssetCloudDeployment: true,
	model.AssetInfrastructure:  true,
	model.AssetDocument:        true,
	model.AssetCorrespondence:  true,
	model.AssetDomain:          true,
	model.AssetHost:            true,
	model.AssetEndpoint:        true,
}

// NewAsset is the input for creating an asset. An empty Sensitivity is inferred from Location.
type NewAsset struct {
	ApplicationID     string
	Type              string
	Location          string
	Sensitivity       string
	Status            string
	Tags              []string
	Metadata          map[string]string
	Origin            string
	VerificationState string
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
	status := na.Status
	if status == "" {
		status = model.AssetStatusConfirmed
	}
	origin := na.Origin
	if origin == "" {
		origin = model.AssetOriginManual
	}
	vs := na.VerificationState
	if vs == "" {
		vs = model.AssetVerificationVerified
	}
	tagsJSON, _ := json.Marshal(na.Tags)
	if na.Tags == nil {
		tagsJSON = []byte("[]")
	}
	metaJSON, _ := json.Marshal(na.Metadata)
	if na.Metadata == nil {
		metaJSON = []byte("{}")
	}

	a := model.Asset{
		ID:                uuid.NewString(),
		ApplicationID:     na.ApplicationID,
		Type:              na.Type,
		Location:          na.Location,
		Sensitivity:       sensitivity,
		Status:            status,
		Tags:              na.Tags,
		Metadata:          na.Metadata,
		Origin:            origin,
		VerificationState: vs,
	}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO assets (id, application_id, type, location, sensitivity, status, tags, metadata,
		 origin, verification_state, first_seen, last_seen, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.ApplicationID, a.Type, a.Location, a.Sensitivity, status,
		string(tagsJSON), string(metaJSON), origin, vs, ts, ts, ts, ts); err != nil {
		return model.Asset{}, err
	}
	a.FirstSeen, a.LastSeen = parseTime(ts), parseTime(ts)
	a.CreatedAt, a.UpdatedAt = parseTime(ts), parseTime(ts)
	return a, nil
}

// UpsertAsset creates an asset or updates last_seen and verification_state if it already exists
// (matched by application_id + type + location). Returns the asset and whether it was newly created.
func (db *DB) UpsertAsset(ctx context.Context, na NewAsset) (model.Asset, bool, error) {
	if na.ApplicationID == "" || na.Location == "" {
		return model.Asset{}, false, errors.New("store: asset application id and location required")
	}
	if !validAssetTypes[na.Type] {
		return model.Asset{}, false, fmt.Errorf("store: invalid asset type %q", na.Type)
	}

	var existing model.Asset
	var eco, created, updated, tags, meta, firstSeen, lastSeen string
	err := db.QueryRowContext(ctx,
		`SELECT id, application_id, type, location, sensitivity, ecosystems, status, tags, metadata,
		        origin, verification_state, first_seen, last_seen, created_at, updated_at
		 FROM assets WHERE application_id = ? AND type = ? AND location = ?`,
		na.ApplicationID, na.Type, na.Location).
		Scan(&existing.ID, &existing.ApplicationID, &existing.Type, &existing.Location,
			&existing.Sensitivity, &eco, &existing.Status, &tags, &meta,
			&existing.Origin, &existing.VerificationState, &firstSeen, &lastSeen,
			&created, &updated)
	if err == nil {
		ts := nowString()
		vs := na.VerificationState
		if vs == "" {
			vs = existing.VerificationState
		}
		if _, err := db.ExecContext(ctx,
			`UPDATE assets SET last_seen = ?, verification_state = ?, updated_at = ? WHERE id = ?`,
			ts, vs, ts, existing.ID); err != nil {
			return model.Asset{}, false, err
		}
		existing.Ecosystems = splitTags(eco)
		existing.Tags = parseJSONStringSlice(tags)
		existing.Metadata = parseJSONStringMap(meta)
		existing.VerificationState = vs
		existing.FirstSeen = parseTime(firstSeen)
		existing.LastSeen = parseTime(ts)
		existing.CreatedAt = parseTime(created)
		existing.UpdatedAt = parseTime(ts)
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return model.Asset{}, false, err
	}
	a, err := db.CreateAsset(ctx, na)
	return a, true, err
}

// UpdateAssetSensitivity changes an asset's sensitivity in place and returns the updated asset. The value
// must be non-empty (unlike create, it is not inferred — the caller is editing deliberately); it is
// validated against the classification registry at the API layer, which sees the global scale (the project
// DB does not hold classification_levels), so the store only guards against an empty value here.
func (db *DB) UpdateAssetSensitivity(ctx context.Context, id, sensitivity string) (model.Asset, error) {
	if sensitivity == "" {
		return model.Asset{}, fmt.Errorf("store: sensitivity required")
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

// DeleteAsset removes an asset. Tasks and playbook runs that referenced it keep their rows with a null
// asset_id (the FK is ON DELETE SET NULL), so prior scan provenance survives the deletion.
func (db *DB) DeleteAsset(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM assets WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

const assetColumns = `id, application_id, type, location, sensitivity, ecosystems, status, tags,
	metadata, origin, verification_state, first_seen, last_seen, created_at, updated_at`

// ListAssetsByApplication returns an application's assets, oldest first.
func (db *DB) ListAssetsByApplication(ctx context.Context, applicationID string) ([]model.Asset, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+assetColumns+` FROM assets WHERE application_id = ? ORDER BY created_at`, applicationID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanAssets(rows)
}

// ListAssets returns all assets, newest first (used by the Analyst to find scan targets).
func (db *DB) ListAssets(ctx context.Context) ([]model.Asset, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+assetColumns+` FROM assets ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanAssets(rows)
}

// GetAsset returns an asset by id.
func (db *DB) GetAsset(ctx context.Context, id string) (model.Asset, error) {
	var a model.Asset
	var eco, created, updated, tags, meta, firstSeen, lastSeen string
	err := db.QueryRowContext(ctx,
		`SELECT `+assetColumns+` FROM assets WHERE id = ?`, id).
		Scan(&a.ID, &a.ApplicationID, &a.Type, &a.Location, &a.Sensitivity, &eco,
			&a.Status, &tags, &meta, &a.Origin, &a.VerificationState,
			&firstSeen, &lastSeen, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Asset{}, ErrNotFound
	}
	if err != nil {
		return model.Asset{}, err
	}
	a.Ecosystems = splitTags(eco)
	a.Tags = parseJSONStringSlice(tags)
	a.Metadata = parseJSONStringMap(meta)
	a.FirstSeen, a.LastSeen = parseTime(firstSeen), parseTime(lastSeen)
	a.CreatedAt, a.UpdatedAt = parseTime(created), parseTime(updated)
	return a, nil
}

func scanAssets(rows *sql.Rows) ([]model.Asset, error) {
	var out []model.Asset
	for rows.Next() {
		var a model.Asset
		var eco, created, updated, tags, meta, firstSeen, lastSeen string
		if err := rows.Scan(&a.ID, &a.ApplicationID, &a.Type, &a.Location, &a.Sensitivity, &eco,
			&a.Status, &tags, &meta, &a.Origin, &a.VerificationState,
			&firstSeen, &lastSeen, &created, &updated); err != nil {
			return nil, err
		}
		a.Ecosystems = splitTags(eco)
		a.Tags = parseJSONStringSlice(tags)
		a.Metadata = parseJSONStringMap(meta)
		a.FirstSeen, a.LastSeen = parseTime(firstSeen), parseTime(lastSeen)
		a.CreatedAt, a.UpdatedAt = parseTime(created), parseTime(updated)
		out = append(out, a)
	}
	return out, rows.Err()
}

// SetAssetEcosystems replaces an asset's manual ecosystem tags (normalized: lowercased, trimmed, deduped).
func (db *DB) SetAssetEcosystems(ctx context.Context, id string, ecosystems []string) (model.Asset, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE assets SET ecosystems = ?, updated_at = ? WHERE id = ?`,
		joinTags(ecosystems), nowString(), id)
	if err != nil {
		return model.Asset{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.Asset{}, ErrNotFound
	}
	return db.GetAsset(ctx, id)
}

// splitTags parses a comma-separated tag list; joinTags normalizes and serializes one.
func splitTags(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func joinTags(tags []string) string {
	seen := map[string]bool{}
	var out []string
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return strings.Join(out, ",")
}

func parseJSONStringSlice(s string) []string {
	if s == "" || s == "[]" {
		return nil
	}
	var out []string
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

func parseJSONStringMap(s string) map[string]string {
	if s == "" || s == "{}" {
		return nil
	}
	var out map[string]string
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

// UpdateAssetStatus changes an asset's lifecycle status.
func (db *DB) UpdateAssetStatus(ctx context.Context, id, status string) (model.Asset, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE assets SET status = ?, updated_at = ? WHERE id = ?`,
		status, nowString(), id)
	if err != nil {
		return model.Asset{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.Asset{}, ErrNotFound
	}
	return db.GetAsset(ctx, id)
}

// SetAssetTags replaces an asset's investigation tags (JSON array).
func (db *DB) SetAssetTags(ctx context.Context, id string, tags []string) (model.Asset, error) {
	normalized := make([]string, 0, len(tags))
	seen := map[string]bool{}
	for _, t := range tags {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		normalized = append(normalized, t)
	}
	b, _ := json.Marshal(normalized)
	res, err := db.ExecContext(ctx,
		`UPDATE assets SET tags = ?, updated_at = ? WHERE id = ?`,
		string(b), nowString(), id)
	if err != nil {
		return model.Asset{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.Asset{}, ErrNotFound
	}
	return db.GetAsset(ctx, id)
}

// UpdateAssetVerification changes an asset's verification state.
func (db *DB) UpdateAssetVerification(ctx context.Context, id, state string) (model.Asset, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE assets SET verification_state = ?, updated_at = ? WHERE id = ?`,
		state, nowString(), id)
	if err != nil {
		return model.Asset{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.Asset{}, ErrNotFound
	}
	return db.GetAsset(ctx, id)
}
