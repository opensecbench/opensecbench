package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// projectTaskFilter matches tasks belonging to a project either directly (project_id) or via their
// application. It is a SQL fragment with two "?" placeholders both bound to the project id.
const projectTaskFilter = `(t.project_id = ? OR t.application_id IN (SELECT id FROM applications WHERE project_id = ?))`

// ListObservationsByProject returns a project's observations: those attached directly (project_id, e.g.
// integration pull) and those from the project's tasks (direct project_id or via application).
func (db *DB) ListObservationsByProject(ctx context.Context, projectID string) ([]model.Observation, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT o.id, o.task_id, o.artifact_id, o.project_id, o.origin, o.review_state, o.title, o.detail, o.severity, o.rule_id, o.location, o.created_at
		 FROM observations o LEFT JOIN tasks t ON o.task_id = t.id
		 WHERE o.project_id = ? OR `+projectTaskFilter+` ORDER BY o.created_at`, projectID, projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanObservations(rows)
}

// LatestArtifactSHA returns the sha256 of the newest successful output artifact produced by the
// given capability within a project (used to fetch the current SBOM for the dependency graph).
func (db *DB) LatestArtifactSHA(ctx context.Context, projectID, capabilityID string) (string, error) {
	var sha string
	err := db.QueryRowContext(ctx,
		`SELECT a.sha256 FROM artifacts a JOIN tasks t ON a.task_id = t.id
		 WHERE `+projectTaskFilter+` AND t.capability_id = ? AND t.status = 'succeeded' AND a.kind = 'output'
		 ORDER BY a.created_at DESC LIMIT 1`, projectID, projectID, capabilityID).Scan(&sha)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	return sha, err
}
