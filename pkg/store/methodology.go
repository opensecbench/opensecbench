package store

import (
	"context"
	"fmt"
	"strings"

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

// MethodologyItemFindings is the "what we found" signal for a methodology item: how many findings are linked
// to it through its evidence observations, and the worst severity among them (ADR-0056 P3).
type MethodologyItemFindings struct {
	Count         int
	WorstSeverity string
}

func severityRank(s string) int {
	switch s {
	case "critical":
		return 5
	case "high":
		return 4
	case "medium":
		return 3
	case "low":
		return 2
	case "info":
		return 1
	}
	return 0
}

// FindingsByMethodologyItem counts the non-false-positive findings linked to each methodology item through its
// evidence observations, with the worst severity per item (ADR-0056 P3). Coverage tracks "tested"; this is the
// separate "what we found" signal shown alongside it, so an item can be fully tested and still carry a finding.
func (db *DB) FindingsByMethodologyItem(ctx context.Context, projectID string) (map[string]MethodologyItemFindings, error) {
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT co.item_id, f.id, f.severity
		FROM coverage_observations co
		JOIN finding_observations fo ON fo.observation_id = co.observation_id
		JOIN findings f ON f.id = fo.finding_id
		WHERE co.project_id = ? AND f.status != 'false_positive'`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	perItem := map[string]map[string]string{} // item id -> finding id -> severity (dedup findings per item)
	for rows.Next() {
		var item, findingID, sev string
		if err := rows.Scan(&item, &findingID, &sev); err != nil {
			return nil, err
		}
		if perItem[item] == nil {
			perItem[item] = map[string]string{}
		}
		perItem[item][findingID] = sev
	}
	out := make(map[string]MethodologyItemFindings, len(perItem))
	for item, fs := range perItem {
		worst := ""
		for _, sev := range fs {
			if severityRank(sev) > severityRank(worst) {
				worst = sev
			}
		}
		out[item] = MethodologyItemFindings{Count: len(fs), WorstSeverity: worst}
	}
	return out, rows.Err()
}

// ActiveMethodologyItemStates returns, per methodology item id, its live run state in the project — "running"
// if any task for that item is executing, else "queued" if one is pending (ADR-0056). Feeds the coverage
// view's transient RunState so the control panel shows items in flight.
func (db *DB) ActiveMethodologyItemStates(ctx context.Context, projectID string) (map[string]string, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT methodology_item_id, status FROM tasks
		 WHERE project_id = ? AND methodology_item_id IS NOT NULL AND status IN ('pending','running')`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]string{}
	for rows.Next() {
		var item, status string
		if err := rows.Scan(&item, &status); err != nil {
			return nil, err
		}
		if status == "running" || out[item] == "" {
			if status == "running" {
				out[item] = "running"
			} else if out[item] == "" {
				out[item] = "queued"
			}
		}
	}
	return out, rows.Err()
}

// DeleteCoverageForItems removes coverage and linked-observation rows for the given methodology item ids in a
// project. Used when a methodology pack is deleted so its per-item coverage doesn't dangle (ADR-0055). A
// nil/empty itemIDs is a no-op.
func (db *DB) DeleteCoverageForItems(ctx context.Context, projectID string, itemIDs []string) error {
	if len(itemIDs) == 0 {
		return nil
	}
	ph := make([]string, len(itemIDs))
	args := make([]any, 0, len(itemIDs)+1)
	args = append(args, projectID)
	for i, id := range itemIDs {
		ph[i] = "?"
		args = append(args, id)
	}
	in := strings.Join(ph, ",")
	if _, err := db.ExecContext(ctx, `DELETE FROM methodology_coverage WHERE project_id = ? AND item_id IN (`+in+`)`, args...); err != nil {
		return err
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM coverage_observations WHERE project_id = ? AND item_id IN (`+in+`)`, args...); err != nil {
		return err
	}
	return nil
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
