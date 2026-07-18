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
	if e.TargetID == "" || e.Title == "" || e.Kind == "" {
		return model.KBEntry{}, errors.New("store: kb entry target id, kind, and title required")
	}
	if e.ID == "" {
		e.ID = uuid.NewString()
	}
	if e.Scope == "" {
		e.Scope = model.KBScopeTarget
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
		`INSERT INTO kb_entries (id, target_id, kind, scope, title, body, tags, sensitivity, origin, review_state, source_ref, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		e.ID, e.TargetID, e.Kind, e.Scope, e.Title, e.Body, e.Tags, e.Sensitivity, e.Origin, e.ReviewState, e.SourceRef, ts, ts); err != nil {
		return model.KBEntry{}, err
	}
	e.CreatedAt, e.UpdatedAt = parseTime(ts), parseTime(ts)
	return e, nil
}

const kbCols = `id, target_id, kind, scope, title, body, tags, sensitivity, origin, review_state, source_ref, created_at, updated_at`

func scanKB(s interface{ Scan(...any) error }) (model.KBEntry, error) {
	var e model.KBEntry
	var created, updated string
	if err := s.Scan(&e.ID, &e.TargetID, &e.Kind, &e.Scope, &e.Title, &e.Body, &e.Tags,
		&e.Sensitivity, &e.Origin, &e.ReviewState, &e.SourceRef, &created, &updated); err != nil {
		return model.KBEntry{}, err
	}
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

// ListKBByTarget returns a target's KB entries, newest first.
func (db *DB) ListKBByTarget(ctx context.Context, targetID string) ([]model.KBEntry, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+kbCols+` FROM kb_entries WHERE target_id = ? ORDER BY updated_at DESC`, targetID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanKBRows(rows)
}

// ListKBByProject returns KB entries for every target the project references (inheritance).
func (db *DB) ListKBByProject(ctx context.Context, projectID string) ([]model.KBEntry, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+kbCols+` FROM kb_entries
		 WHERE target_id IN (SELECT target_id FROM project_targets WHERE project_id = ?)
		 ORDER BY updated_at DESC`, projectID)
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
