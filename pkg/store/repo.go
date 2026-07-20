package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// ErrNotFound is returned when a requested record does not exist.
var ErrNotFound = errors.New("store: not found")

const timeLayout = time.RFC3339Nano

func nowString() string { return time.Now().UTC().Format(timeLayout) }

func parseTime(s string) time.Time {
	t, _ := time.Parse(timeLayout, s)
	return t
}

func ptr(ns sql.NullString) *string {
	if !ns.Valid {
		return nil
	}
	v := ns.String
	return &v
}

// --- Organizations ---

// CreateOrganization inserts a new organization.
func (db *DB) CreateOrganization(ctx context.Context, name string) (model.Organization, error) {
	if name == "" {
		return model.Organization{}, errors.New("store: organization name required")
	}
	o := model.Organization{ID: uuid.NewString(), Name: name}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO organizations (id, name, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		o.ID, o.Name, ts, ts); err != nil {
		return model.Organization{}, err
	}
	o.CreatedAt, o.UpdatedAt = parseTime(ts), parseTime(ts)
	return o, nil
}

// ListOrganizations returns all organizations ordered by name.
func (db *DB) ListOrganizations(ctx context.Context) ([]model.Organization, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, name, created_at, updated_at FROM organizations ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.Organization
	for rows.Next() {
		var o model.Organization
		var created, updated string
		if err := rows.Scan(&o.ID, &o.Name, &created, &updated); err != nil {
			return nil, err
		}
		o.CreatedAt, o.UpdatedAt = parseTime(created), parseTime(updated)
		out = append(out, o)
	}
	return out, rows.Err()
}

// --- Targets ---

// CreateTarget inserts a durable target, optionally under an organization.
func (db *DB) CreateTarget(ctx context.Context, name, description string, organizationID *string) (model.Target, error) {
	if name == "" {
		return model.Target{}, errors.New("store: target name required")
	}
	t := model.Target{ID: uuid.NewString(), OrganizationID: organizationID, Name: name, Description: description}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO targets (id, organization_id, name, description, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		t.ID, organizationID, t.Name, t.Description, ts, ts); err != nil {
		return model.Target{}, err
	}
	t.CreatedAt, t.UpdatedAt = parseTime(ts), parseTime(ts)
	return t, nil
}

// ListTargets returns all targets ordered by name.
// GetTarget returns one target by id.
func (db *DB) GetTarget(ctx context.Context, id string) (model.Target, error) {
	var t model.Target
	var org sql.NullString
	var created, updated string
	err := db.QueryRowContext(ctx,
		`SELECT id, organization_id, name, description, created_at, updated_at FROM targets WHERE id = ?`, id).
		Scan(&t.ID, &org, &t.Name, &t.Description, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Target{}, ErrNotFound
	}
	if err != nil {
		return model.Target{}, err
	}
	t.OrganizationID = ptr(org)
	t.CreatedAt, t.UpdatedAt = parseTime(created), parseTime(updated)
	return t, nil
}

func (db *DB) ListTargets(ctx context.Context) ([]model.Target, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, organization_id, name, description, created_at, updated_at FROM targets ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.Target
	for rows.Next() {
		var t model.Target
		var org sql.NullString
		var created, updated string
		if err := rows.Scan(&t.ID, &org, &t.Name, &t.Description, &created, &updated); err != nil {
			return nil, err
		}
		t.OrganizationID = ptr(org)
		t.CreatedAt, t.UpdatedAt = parseTime(created), parseTime(updated)
		out = append(out, t)
	}
	return out, rows.Err()
}

// --- Projects ---

// NewProject is the input for creating a project (engagement).
type NewProject struct {
	Name           string
	OrganizationID *string
	GroupID        *string
	TargetIDs      []string
}

// CreateProject inserts a project and its target links atomically.
func (db *DB) CreateProject(ctx context.Context, np NewProject) (model.Project, error) {
	return db.CreateProjectWithID(ctx, uuid.NewString(), np)
}

// CreateProjectWithID is CreateProject with a caller-supplied id, so the two-tier Manager can name the
// project's directory before creating its database (ADR-0049).
func (db *DB) CreateProjectWithID(ctx context.Context, id string, np NewProject) (model.Project, error) {
	if np.Name == "" {
		return model.Project{}, errors.New("store: project name required")
	}
	p := model.Project{
		ID:             id,
		OrganizationID: np.OrganizationID,
		GroupID:        np.GroupID,
		Name:           np.Name,
		Status:         "active",
		TargetIDs:      np.TargetIDs,
	}
	ts := nowString()

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return model.Project{}, err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO projects (id, organization_id, group_id, name, status, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		p.ID, np.OrganizationID, np.GroupID, p.Name, p.Status, ts, ts); err != nil {
		return model.Project{}, err
	}
	for _, tid := range np.TargetIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO project_targets (project_id, target_id) VALUES (?, ?)`, p.ID, tid); err != nil {
			return model.Project{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return model.Project{}, err
	}
	p.CreatedAt, p.UpdatedAt = parseTime(ts), parseTime(ts)
	return p, nil
}

// GetProject returns a project by id, including its target links. Returns ErrNotFound if absent.
func (db *DB) GetProject(ctx context.Context, id string) (model.Project, error) {
	var p model.Project
	var org, group sql.NullString
	var created, updated string
	err := db.QueryRowContext(ctx,
		`SELECT id, organization_id, group_id, name, status, created_at, updated_at
		 FROM projects WHERE id = ?`, id).
		Scan(&p.ID, &org, &group, &p.Name, &p.Status, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Project{}, ErrNotFound
	}
	if err != nil {
		return model.Project{}, err
	}
	p.OrganizationID, p.GroupID = ptr(org), ptr(group)
	p.CreatedAt, p.UpdatedAt = parseTime(created), parseTime(updated)

	p.TargetIDs, err = db.projectTargetIDs(ctx, id)
	if err != nil {
		return model.Project{}, err
	}
	return p, nil
}

// ListProjects returns all projects (target links not populated) ordered newest-first.
func (db *DB) ListProjects(ctx context.Context) ([]model.Project, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, organization_id, group_id, name, status, created_at, updated_at
		 FROM projects ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.Project
	for rows.Next() {
		var p model.Project
		var org, group sql.NullString
		var created, updated string
		if err := rows.Scan(&p.ID, &org, &group, &p.Name, &p.Status, &created, &updated); err != nil {
			return nil, err
		}
		p.OrganizationID, p.GroupID = ptr(org), ptr(group)
		p.CreatedAt, p.UpdatedAt = parseTime(created), parseTime(updated)
		out = append(out, p)
	}
	return out, rows.Err()
}

// DeleteProject removes a project (and, by cascade, its applications/assets/target links).
func (db *DB) DeleteProject(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM projects WHERE id = ?`, id)
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

func (db *DB) projectTargetIDs(ctx context.Context, projectID string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT target_id FROM project_targets WHERE project_id = ? ORDER BY target_id`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
