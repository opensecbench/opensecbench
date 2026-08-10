package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

var validResearchTypes = map[string]bool{
	model.ResearchNote:       true,
	model.ResearchHypothesis: true,
	model.ResearchLead:       true,
	model.ResearchQuestion:   true,
	model.ResearchExperiment: true,
	model.ResearchResult:     true,
	model.ResearchConclusion: true,
}

// NewResearchItem is the input for creating a research item. Only Type and Title are required.
type NewResearchItem struct {
	ProjectID  string
	Type       string
	Title      string
	Body       string
	Status     string
	Assessment string
	CreatedBy  string
	Tags       []string
}

// CreateResearchItem inserts a research item.
func (db *DB) CreateResearchItem(ctx context.Context, nr NewResearchItem) (model.ResearchItem, error) {
	if nr.ProjectID == "" || nr.Title == "" {
		return model.ResearchItem{}, errors.New("store: research item requires project_id and title")
	}
	if !validResearchTypes[nr.Type] {
		return model.ResearchItem{}, fmt.Errorf("store: invalid research item type %q", nr.Type)
	}
	status := nr.Status
	if status == "" {
		status = "open"
	}
	createdBy := nr.CreatedBy
	if createdBy == "" {
		createdBy = "manual"
	}
	tagsJSON, _ := json.Marshal(nr.Tags)
	if nr.Tags == nil {
		tagsJSON = []byte("[]")
	}

	r := model.ResearchItem{
		ID:         uuid.NewString(),
		ProjectID:  nr.ProjectID,
		Type:       nr.Type,
		Title:      nr.Title,
		Body:       nr.Body,
		Status:     status,
		Assessment: nr.Assessment,
		CreatedBy:  createdBy,
		Tags:       nr.Tags,
	}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO research_items (id, project_id, type, title, body, status, assessment, created_by, tags, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.ProjectID, r.Type, r.Title, r.Body, status, r.Assessment, createdBy,
		string(tagsJSON), ts, ts); err != nil {
		return model.ResearchItem{}, err
	}
	r.CreatedAt, r.UpdatedAt = parseTime(ts), parseTime(ts)
	return r, nil
}

// GetResearchItem returns a research item by id.
func (db *DB) GetResearchItem(ctx context.Context, id string) (model.ResearchItem, error) {
	var r model.ResearchItem
	var tags, created, updated string
	err := db.QueryRowContext(ctx,
		`SELECT id, project_id, type, title, body, status, assessment, created_by, tags, created_at, updated_at
		 FROM research_items WHERE id = ?`, id).
		Scan(&r.ID, &r.ProjectID, &r.Type, &r.Title, &r.Body, &r.Status, &r.Assessment,
			&r.CreatedBy, &tags, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ResearchItem{}, ErrNotFound
	}
	if err != nil {
		return model.ResearchItem{}, err
	}
	r.Tags = parseJSONStringSlice(tags)
	r.CreatedAt, r.UpdatedAt = parseTime(created), parseTime(updated)
	return r, nil
}

// ListResearchItems returns all research items for a project, newest first.
func (db *DB) ListResearchItems(ctx context.Context, projectID string) ([]model.ResearchItem, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, type, title, body, status, assessment, created_by, tags, created_at, updated_at
		 FROM research_items WHERE project_id = ? ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.ResearchItem
	for rows.Next() {
		var r model.ResearchItem
		var tags, created, updated string
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Type, &r.Title, &r.Body, &r.Status, &r.Assessment,
			&r.CreatedBy, &tags, &created, &updated); err != nil {
			return nil, err
		}
		r.Tags = parseJSONStringSlice(tags)
		r.CreatedAt, r.UpdatedAt = parseTime(created), parseTime(updated)
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateResearchItem updates a research item's mutable fields.
func (db *DB) UpdateResearchItem(ctx context.Context, id string, updates ResearchItemUpdate) (model.ResearchItem, error) {
	existing, err := db.GetResearchItem(ctx, id)
	if err != nil {
		return model.ResearchItem{}, err
	}
	if updates.Title != nil {
		existing.Title = *updates.Title
	}
	if updates.Body != nil {
		existing.Body = *updates.Body
	}
	if updates.Status != nil {
		existing.Status = *updates.Status
	}
	if updates.Assessment != nil {
		existing.Assessment = *updates.Assessment
	}
	if updates.Tags != nil {
		existing.Tags = *updates.Tags
	}
	tagsJSON, _ := json.Marshal(existing.Tags)
	if existing.Tags == nil {
		tagsJSON = []byte("[]")
	}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`UPDATE research_items SET title = ?, body = ?, status = ?, assessment = ?, tags = ?, updated_at = ? WHERE id = ?`,
		existing.Title, existing.Body, existing.Status, existing.Assessment, string(tagsJSON), ts, id); err != nil {
		return model.ResearchItem{}, err
	}
	existing.UpdatedAt = parseTime(ts)
	return existing, nil
}

// ResearchItemUpdate holds optional fields for a partial update.
type ResearchItemUpdate struct {
	Title      *string
	Body       *string
	Status     *string
	Assessment *string
	Tags       *[]string
}

// DeleteResearchItem removes a research item.
func (db *DB) DeleteResearchItem(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM research_items WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
