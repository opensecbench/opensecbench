package store

import (
	"context"
	"fmt"

	"github.com/opensecbench/opensecbench/pkg/model"
)

var validCoverageStatus = map[string]bool{
	model.CoverageNotStarted:    true,
	model.CoverageInProgress:    true,
	model.CoverageCovered:       true,
	model.CoverageNotApplicable: true,
}

// AdoptMethodology marks a methodology pack as in-use for a project (idempotent).
func (db *DB) AdoptMethodology(ctx context.Context, projectID, methodologyID string) error {
	if projectID == "" || methodologyID == "" {
		return fmt.Errorf("store: project id and methodology id required")
	}
	_, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO project_methodologies (project_id, methodology_id, created_at) VALUES (?, ?, ?)`,
		projectID, methodologyID, nowString())
	return err
}

// UnadoptMethodology removes a methodology pack from a project.
func (db *DB) UnadoptMethodology(ctx context.Context, projectID, methodologyID string) error {
	_, err := db.ExecContext(ctx,
		`DELETE FROM project_methodologies WHERE project_id = ? AND methodology_id = ?`, projectID, methodologyID)
	return err
}

// ListAdoptedMethodologies returns the methodology ids a project has adopted.
func (db *DB) ListAdoptedMethodologies(ctx context.Context, projectID string) ([]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT methodology_id FROM project_methodologies WHERE project_id = ? ORDER BY methodology_id`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetCoverage upserts a project's status + note for a methodology item.
func (db *DB) SetCoverage(ctx context.Context, projectID, itemID, status, note string) error {
	if !validCoverageStatus[status] {
		return fmt.Errorf("store: invalid coverage status %q", status)
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO methodology_coverage (project_id, item_id, status, note, updated_at)
		 VALUES (?, ?, ?, ?, ?)
		 ON CONFLICT(project_id, item_id) DO UPDATE SET status = excluded.status, note = excluded.note, updated_at = excluded.updated_at`,
		projectID, itemID, status, note, nowString())
	return err
}

// LinkCoverageObservation attaches an observation (evidence) to a methodology item (idempotent),
// so evidence gathered while testing an item flows back onto that checklist item.
func (db *DB) LinkCoverageObservation(ctx context.Context, projectID, itemID, observationID string) error {
	if projectID == "" || itemID == "" || observationID == "" {
		return fmt.Errorf("store: project id, item id and observation id required")
	}
	_, err := db.ExecContext(ctx,
		`INSERT OR IGNORE INTO coverage_observations (project_id, item_id, observation_id, created_at) VALUES (?, ?, ?, ?)`,
		projectID, itemID, observationID, nowString())
	return err
}

// CountCoverageEvidence returns, per item id, how many observations are attached for a project.
func (db *DB) CountCoverageEvidence(ctx context.Context, projectID string) (map[string]int, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT item_id, COUNT(*) FROM coverage_observations WHERE project_id = ? GROUP BY item_id`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := make(map[string]int)
	for rows.Next() {
		var itemID string
		var n int
		if err := rows.Scan(&itemID, &n); err != nil {
			return nil, err
		}
		out[itemID] = n
	}
	return out, rows.Err()
}

// ListCoverage returns a project's recorded coverage entries.
func (db *DB) ListCoverage(ctx context.Context, projectID string) ([]model.CoverageEntry, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT project_id, item_id, status, note, updated_at FROM methodology_coverage WHERE project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.CoverageEntry
	for rows.Next() {
		var e model.CoverageEntry
		var updated string
		if err := rows.Scan(&e.ProjectID, &e.ItemID, &e.Status, &e.Note, &updated); err != nil {
			return nil, err
		}
		e.UpdatedAt = parseTime(updated)
		out = append(out, e)
	}
	return out, rows.Err()
}
