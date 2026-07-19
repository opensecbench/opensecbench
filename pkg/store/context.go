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

var validContextTypes = map[string]bool{
	model.ContextDocument: true,
	model.ContextEmail:    true,
	model.ContextChat:     true,
	model.ContextNote:     true,
}

// CreateContextItem records an ingested context item. Its bytes must already be stored in the CAS
// and referenced by ArtifactID.
func (db *DB) CreateContextItem(ctx context.Context, ci model.ContextItem) (model.ContextItem, error) {
	if ci.ProjectID == "" || ci.Name == "" || ci.ArtifactID == "" {
		return model.ContextItem{}, errors.New("store: context item project id, name, and artifact id required")
	}
	if ci.Type == "" {
		ci.Type = model.ContextDocument
	}
	if !validContextTypes[ci.Type] {
		return model.ContextItem{}, fmt.Errorf("store: invalid context type %q", ci.Type)
	}
	if ci.ID == "" {
		ci.ID = uuid.NewString()
	}
	ci.CreatedAt = time.Now().UTC()
	_, err := db.ExecContext(ctx,
		`INSERT INTO context_items (id, project_id, application_id, type, name, artifact_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		ci.ID, ci.ProjectID, ci.ApplicationID, ci.Type, ci.Name, ci.ArtifactID, ci.CreatedAt.Format(timeLayout))
	if err != nil {
		return model.ContextItem{}, err
	}
	return ci, nil
}

// GetContextItem returns one ingested context item by id.
func (db *DB) GetContextItem(ctx context.Context, id string) (model.ContextItem, error) {
	var ci model.ContextItem
	var app sql.NullString
	var created string
	err := db.QueryRowContext(ctx,
		`SELECT id, project_id, application_id, type, name, artifact_id, created_at
		 FROM context_items WHERE id = ?`, id).Scan(&ci.ID, &ci.ProjectID, &app, &ci.Type, &ci.Name, &ci.ArtifactID, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ContextItem{}, ErrNotFound
	}
	if err != nil {
		return model.ContextItem{}, err
	}
	ci.ApplicationID = ptr(app)
	ci.CreatedAt = parseTime(created)
	return ci, nil
}

// ListContextItemsByProject returns a project's context items, newest first.
func (db *DB) ListContextItemsByProject(ctx context.Context, projectID string) ([]model.ContextItem, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, application_id, type, name, artifact_id, created_at
		 FROM context_items WHERE project_id = ? ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.ContextItem
	for rows.Next() {
		var ci model.ContextItem
		var app sql.NullString
		var created string
		if err := rows.Scan(&ci.ID, &ci.ProjectID, &app, &ci.Type, &ci.Name, &ci.ArtifactID, &created); err != nil {
			return nil, err
		}
		ci.ApplicationID = ptr(app)
		ci.CreatedAt = parseTime(created)
		out = append(out, ci)
	}
	return out, rows.Err()
}
