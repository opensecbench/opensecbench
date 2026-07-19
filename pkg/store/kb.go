package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// CreateKBEntry inserts a knowledge-base entry. Human entries default to confirmed; agent-drafted
// (origin=thread) default to unreviewed, matching the observation review discipline (ADR-0005).
func (db *DB) CreateKBEntry(ctx context.Context, e model.KBEntry) (model.KBEntry, error) {
	if e.Title == "" || e.Kind == "" {
		return model.KBEntry{}, errors.New("store: kb entry kind and title required")
	}
	if e.Scope == "" {
		e.Scope = model.KBScopeTarget
	}
	// Anchor by scope: exactly the right id must be set (ADR-0041).
	switch e.Scope {
	case model.KBScopeTarget:
		if e.TargetID == "" {
			return model.KBEntry{}, errors.New("store: target-scoped kb entry needs a target id")
		}
		e.GroupID, e.OrganizationID = "", ""
	case model.KBScopeGroup:
		if e.GroupID == "" {
			return model.KBEntry{}, errors.New("store: group-scoped kb entry needs a group id")
		}
		e.TargetID, e.OrganizationID = "", ""
	case model.KBScopeOrg:
		if e.OrganizationID == "" {
			return model.KBEntry{}, errors.New("store: org-scoped kb entry needs an organization id")
		}
		e.TargetID, e.GroupID = "", ""
	case model.KBScopeGlobal:
		e.TargetID, e.GroupID, e.OrganizationID = "", "", ""
	default:
		return model.KBEntry{}, errors.New("store: invalid kb scope " + e.Scope)
	}
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.Origin == "" {
		e.Origin = model.OriginHuman
	}
	if e.Sensitivity == "" {
		e.Sensitivity = model.SensitivityPrivate
	}
	if e.ReviewState == "" {
		if e.Origin == model.OriginHuman {
			e.ReviewState = model.ReviewConfirmed
		} else {
			e.ReviewState = model.ReviewUnreviewed
		}
	}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO kb_entries (id, scope, target_id, group_id, organization_id, kind, title, body, tags, sensitivity, origin, review_state, source_ref, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.Scope, nullable(e.TargetID), nullable(e.GroupID), nullable(e.OrganizationID), e.Kind, e.Title, e.Body, e.Tags, e.Sensitivity, e.Origin, e.ReviewState, e.SourceRef, ts, ts); err != nil {
		return model.KBEntry{}, err
	}
	e.CreatedAt, e.UpdatedAt = parseTime(ts), parseTime(ts)
	return e, nil
}

// nullable turns "" into a SQL NULL so a NOT-set anchor column is NULL (satisfying the scope CHECK).
func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

const kbCols = `id, scope, target_id, group_id, organization_id, kind, title, body, tags, sensitivity, origin, review_state, source_ref, created_at, updated_at`

// scopeOrder ranks entries most-specific first (target < group < org < global) for the inheritance walk.
const scopeOrder = `CASE scope WHEN 'target' THEN 0 WHEN 'group' THEN 1 WHEN 'org' THEN 2 ELSE 3 END`

func scanKB(s interface{ Scan(...any) error }) (model.KBEntry, error) {
	var e model.KBEntry
	var created, updated string
	var target, group, org sql.NullString
	if err := s.Scan(&e.ID, &e.Scope, &target, &group, &org, &e.Kind, &e.Title, &e.Body, &e.Tags,
		&e.Sensitivity, &e.Origin, &e.ReviewState, &e.SourceRef, &created, &updated); err != nil {
		return model.KBEntry{}, err
	}
	e.TargetID, e.GroupID, e.OrganizationID = target.String, group.String, org.String
	e.CreatedAt, e.UpdatedAt = parseTime(created), parseTime(updated)
	return e, nil
}

// GetKBEntry returns one entry by id.
func (db *DB) GetKBEntry(ctx context.Context, id string) (model.KBEntry, error) {
	row := db.QueryRowContext(ctx, `SELECT `+kbCols+` FROM kb_entries WHERE id = ?`, id)
	e, err := scanKB(row)
	if errors.Is(err, sql.ErrNoRows) {
		return model.KBEntry{}, ErrNotFound
	}
	return e, err
}

// ListKBByTarget returns a target's knowledge with inheritance (ADR-0041): the target's own entries, the
// entries scoped to the target's organization, and global entries — most-specific first.
func (db *DB) ListKBByTarget(ctx context.Context, targetID string) ([]model.KBEntry, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+kbCols+` FROM kb_entries WHERE
		    (scope = 'target' AND target_id = ?)
		 OR (scope = 'org' AND organization_id = (SELECT organization_id FROM targets WHERE id = ? AND organization_id IS NOT NULL))
		 OR scope = 'global'
		 ORDER BY `+scopeOrder+`, updated_at DESC`, targetID, targetID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanKBRows(rows)
}

// ListKBByProject returns the knowledge a project inherits (ADR-0041): its target(s) + its group + its
// organization (and the organizations of its targets) + global — most-specific first. So re-assessing a
// known org starts with everything the team learned before, not just the one target.
func (db *DB) ListKBByProject(ctx context.Context, projectID string) ([]model.KBEntry, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+kbCols+` FROM kb_entries WHERE
		    (scope = 'target' AND target_id IN (SELECT target_id FROM project_targets WHERE project_id = ?))
		 OR (scope = 'group' AND group_id = (SELECT group_id FROM projects WHERE id = ?))
		 OR (scope = 'org' AND organization_id IN (
		        SELECT organization_id FROM projects WHERE id = ? AND organization_id IS NOT NULL
		        UNION
		        SELECT t.organization_id FROM targets t JOIN project_targets pt ON t.id = pt.target_id
		         WHERE pt.project_id = ? AND t.organization_id IS NOT NULL))
		 OR scope = 'global'
		 ORDER BY `+scopeOrder+`, updated_at DESC`, projectID, projectID, projectID, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanKBRows(rows)
}

func scanKBRows(rows *sql.Rows) ([]model.KBEntry, error) {
	var out []model.KBEntry
	for rows.Next() {
		e, err := scanKB(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ReviewKBEntry sets an entry's review state (confirmed | rejected | unreviewed).
func (db *DB) ReviewKBEntry(ctx context.Context, id, state string) error {
	switch state {
	case model.ReviewConfirmed, model.ReviewRejected, model.ReviewUnreviewed:
	default:
		return errors.New("store: invalid kb review state")
	}
	res, err := db.ExecContext(ctx, `UPDATE kb_entries SET review_state = ?, updated_at = ? WHERE id = ?`,
		state, nowString(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateKBEntry edits an entry's curated fields (human curation).
func (db *DB) UpdateKBEntry(ctx context.Context, id, title, body, tags string) error {
	res, err := db.ExecContext(ctx,
		`UPDATE kb_entries SET title = ?, body = ?, tags = ?, updated_at = ? WHERE id = ?`,
		title, body, tags, nowString(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
