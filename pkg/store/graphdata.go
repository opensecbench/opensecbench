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
		`SELECT o.id, o.task_id, o.artifact_id, o.project_id, o.origin, o.review_state, o.title, o.detail, o.severity, o.rule_id, o.location, o.attributes, o.fingerprint, o.created_at
		 FROM observations o LEFT JOIN tasks t ON o.task_id = t.id
		 WHERE o.project_id = ? OR `+projectTaskFilter+` ORDER BY o.created_at`, projectID, projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanObservations(rows)
}

// LocatedObservation pairs an observation with the source_repo asset its `location` path is relative to,
// resolved through the producing task (observation.task_id → task.asset_id). AssetID is empty when the
// task had no source asset (e.g. a network scan or a bare target_dir run), in which case the UI cannot
// offer click-to-file for it.
type LocatedObservation struct {
	model.Observation
	AssetID string `json:"asset_id,omitempty"`
}

// ListLocatedObservationsByProject returns a project's observations enriched with the source asset that
// their location refers to, so the frontend can turn a "path:line" location into a click-to-file jump.
func (db *DB) ListLocatedObservationsByProject(ctx context.Context, projectID string) ([]LocatedObservation, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT o.id, o.task_id, o.artifact_id, o.project_id, o.origin, o.review_state, o.title, o.detail, o.severity, o.rule_id, o.location, o.attributes, o.fingerprint, o.created_at, COALESCE(t.asset_id, '')
		 FROM observations o LEFT JOIN tasks t ON o.task_id = t.id
		 WHERE o.project_id = ? OR `+projectTaskFilter+` ORDER BY o.created_at`, projectID, projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []LocatedObservation{}
	for rows.Next() {
		var lo LocatedObservation
		var task, artifact, project sql.NullString
		var attrs, created string
		if err := rows.Scan(&lo.ID, &task, &artifact, &project, &lo.Origin, &lo.ReviewState, &lo.Title, &lo.Detail,
			&lo.Severity, &lo.RuleID, &lo.Location, &attrs, &lo.Fingerprint, &created, &lo.AssetID); err != nil {
			return nil, err
		}
		lo.TaskID, lo.ArtifactID, lo.ProjectID = ptr(task), ptr(artifact), ptr(project)
		lo.Attributes = parseAttrs(attrs)
		lo.CreatedAt = parseTime(created)
		out = append(out, lo)
	}
	return out, rows.Err()
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
