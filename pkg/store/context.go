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
		`INSERT INTO context_items (id, project_id, application_id, type, name, artifact_id, tags, pinned, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		ci.ID, ci.ProjectID, ci.ApplicationID, ci.Type, ci.Name, ci.ArtifactID, joinTags(ci.Tags), ci.Pinned, ci.CreatedAt.Format(timeLayout))
	if err != nil {
		return model.ContextItem{}, err
	}
	return ci, nil
}

// GetContextItem returns one ingested context item by id.
func (db *DB) GetContextItem(ctx context.Context, id string) (model.ContextItem, error) {
	var ci model.ContextItem
	var app sql.NullString
	var tags, created string
	err := db.QueryRowContext(ctx,
		`SELECT id, project_id, application_id, type, name, artifact_id, tags, pinned, created_at
		 FROM context_items WHERE id = ?`, id).Scan(&ci.ID, &ci.ProjectID, &app, &ci.Type, &ci.Name, &ci.ArtifactID, &tags, &ci.Pinned, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ContextItem{}, ErrNotFound
	}
	if err != nil {
		return model.ContextItem{}, err
	}
	ci.ApplicationID = ptr(app)
	ci.Tags = splitTags(tags)
	ci.CreatedAt = parseTime(created)
	return ci, nil
}

// UpdateContextItem updates a context item's mutable fields: name, tags, pinned, and artifact_id. The
// artifact_id changes only when a note's body is re-saved (its text is re-stored in the CAS and this points
// at the new blob); metadata-only edits pass the item's existing artifact_id unchanged. Returns ErrNotFound
// when no row matches.
func (db *DB) UpdateContextItem(ctx context.Context, id, name string, tags []string, pinned bool, artifactID string) (model.ContextItem, error) {
	if id == "" || name == "" || artifactID == "" {
		return model.ContextItem{}, errors.New("store: context item id, name, and artifact id required")
	}
	res, err := db.ExecContext(ctx,
		`UPDATE context_items SET name = ?, tags = ?, pinned = ?, artifact_id = ? WHERE id = ?`,
		name, joinTags(tags), pinned, artifactID, id)
	if err != nil {
		return model.ContextItem{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.ContextItem{}, ErrNotFound
	}
	return db.GetContextItem(ctx, id)
}

// DeleteContextItem removes a context item. The CAS blob it referenced is left in place — content-addressed
// storage may be shared by other artifacts, and orphan blobs are harmless. Returns ErrNotFound when no row
// matches.
func (db *DB) DeleteContextItem(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM context_items WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListContextItemsByProject returns a project's context items, newest first.
func (db *DB) ListContextItemsByProject(ctx context.Context, projectID string) ([]model.ContextItem, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, application_id, type, name, artifact_id, tags, pinned, created_at
		 FROM context_items WHERE project_id = ? ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.ContextItem
	for rows.Next() {
		var ci model.ContextItem
		var app sql.NullString
		var tags, created string
		if err := rows.Scan(&ci.ID, &ci.ProjectID, &app, &ci.Type, &ci.Name, &ci.ArtifactID, &tags, &ci.Pinned, &created); err != nil {
			return nil, err
		}
		ci.ApplicationID = ptr(app)
		ci.Tags = splitTags(tags)
		ci.CreatedAt = parseTime(created)
		out = append(out, ci)
	}
	return out, rows.Err()
}
