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
	// A confirmed-on-creation entry (human-authored) is verified as of now; an unreviewed draft is not yet
	// verified (NULL) — it awaits a human confirm (ADR-0043).
	var verified any
	if e.ReviewState == model.ReviewConfirmed {
		verified = ts
		e.LastVerifiedAt = parseTime(ts)
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO kb_entries (id, scope, target_id, group_id, organization_id, kind, title, body, tags, sensitivity, origin, review_state, source_ref, last_verified_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.Scope, nullable(e.TargetID), nullable(e.GroupID), nullable(e.OrganizationID), e.Kind, e.Title, e.Body, e.Tags, e.Sensitivity, e.Origin, e.ReviewState, e.SourceRef, verified, ts, ts); err != nil {
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

const kbCols = `id, scope, target_id, group_id, organization_id, kind, title, body, tags, sensitivity, origin, review_state, source_ref, last_verified_at, created_at, updated_at`

// scopeOrder ranks entries most-specific first (target < group < org < global) for the inheritance walk.
const scopeOrder = `CASE scope WHEN 'target' THEN 0 WHEN 'group' THEN 1 WHEN 'org' THEN 2 ELSE 3 END`

func scanKB(s interface{ Scan(...any) error }) (model.KBEntry, error) {
	var e model.KBEntry
	var created, updated string
	var target, group, org, verified sql.NullString
	if err := s.Scan(&e.ID, &e.Scope, &target, &group, &org, &e.Kind, &e.Title, &e.Body, &e.Tags,
		&e.Sensitivity, &e.Origin, &e.ReviewState, &e.SourceRef, &verified, &created, &updated); err != nil {
		return model.KBEntry{}, err
	}
	e.TargetID, e.GroupID, e.OrganizationID = target.String, group.String, org.String
	e.LastVerifiedAt = parseTime(verified.String) // "" → zero time (never verified)
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

// ListKBByAnchors returns the KB entries visible for the given anchors, resolved most-specific first:
// target entries whose target is in targetIDs, the group entry for groupID, org entries whose org is in
// orgIDs, and all global entries. This is the split-mode form of ListKBByProject — the caller reads the
// anchors from the project's own database and queries the global KB here (ADR-0049).
func (db *DB) ListKBByAnchors(ctx context.Context, targetIDs []string, groupID string, orgIDs []string) ([]model.KBEntry, error) {
	q := `SELECT ` + kbCols + ` FROM kb_entries WHERE scope = 'global'`
	var args []any
	if len(targetIDs) > 0 {
		q += ` OR (scope = 'target' AND target_id IN (` + placeholders(len(targetIDs)) + `))`
		for _, t := range targetIDs {
			args = append(args, t)
		}
	}
	if groupID != "" {
		q += ` OR (scope = 'group' AND group_id = ?)`
		args = append(args, groupID)
	}
	if len(orgIDs) > 0 {
		q += ` OR (scope = 'org' AND organization_id IN (` + placeholders(len(orgIDs)) + `))`
		for _, o := range orgIDs {
			args = append(args, o)
		}
	}
	q += ` ORDER BY ` + scopeOrder + `, updated_at DESC`
	rows, err := db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanKBRows(rows)
}

// placeholders returns "?, ?, ..." with n placeholders for an IN clause.
func placeholders(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, 0, n*3)
	for i := 0; i < n; i++ {
		if i > 0 {
			b = append(b, ',', ' ')
		}
		b = append(b, '?')
	}
	return string(b)
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

// ReviewKBEntry sets an entry's review state (confirmed | rejected | unreviewed). Confirming an entry also
// stamps it as verified now — a human affirming the fact is affirming it currently holds (ADR-0043).
func (db *DB) ReviewKBEntry(ctx context.Context, id, state string) error {
	switch state {
	case model.ReviewConfirmed, model.ReviewRejected, model.ReviewUnreviewed:
	default:
		return errors.New("store: invalid kb review state")
	}
	ts := nowString()
	q := `UPDATE kb_entries SET review_state = ?, updated_at = ? WHERE id = ?`
	args := []any{state, ts, id}
	if state == model.ReviewConfirmed {
		q = `UPDATE kb_entries SET review_state = ?, last_verified_at = ?, updated_at = ? WHERE id = ?`
		args = []any{state, ts, ts, id}
	}
	res, err := db.ExecContext(ctx, q, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// VerifyKBEntry bumps an entry's last-verified timestamp — "this fact still holds as of now" (ADR-0043).
// It does NOT change review state, so it's safe for the agent to call when it re-observes a known fact: a
// draft stays a draft (humans confirm), a confirmed fact just gets its freshness renewed.
func (db *DB) VerifyKBEntry(ctx context.Context, id string) error {
	ts := nowString()
	res, err := db.ExecContext(ctx, `UPDATE kb_entries SET last_verified_at = ?, updated_at = ? WHERE id = ?`,
		ts, ts, id)
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
