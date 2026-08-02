package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/opensecbench/opensecbench/pkg/model"
)

const classificationCols = `id, label, rank, builtin, color`

func scanLevel(s interface{ Scan(...any) error }) (model.ClassificationLevel, error) {
	var l model.ClassificationLevel
	var builtin int
	if err := s.Scan(&l.ID, &l.Label, &l.Rank, &builtin, &l.Color); err != nil {
		return model.ClassificationLevel{}, err
	}
	l.Builtin = builtin != 0
	return l, nil
}

// ListClassificationLevels returns the data-classification scale ordered least-sensitive first.
func (db *DB) ListClassificationLevels(ctx context.Context) ([]model.ClassificationLevel, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+classificationCols+` FROM classification_levels ORDER BY rank ASC, id ASC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.ClassificationLevel
	for rows.Next() {
		l, err := scanLevel(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// LoadScale reads the levels and builds the Scale used for egress decisions. On error it returns an empty
// scale, which fails safe (every non-least tier is blocked).
func (db *DB) LoadScale(ctx context.Context) model.Scale {
	levels, _ := db.ListClassificationLevels(ctx)
	return model.NewScale(levels)
}

// CreateClassificationLevel adds a custom level (builtin is always false here). id must be a unique slug.
func (db *DB) CreateClassificationLevel(ctx context.Context, l model.ClassificationLevel) (model.ClassificationLevel, error) {
	if l.ID == "" || l.Label == "" {
		return model.ClassificationLevel{}, fmt.Errorf("store: classification level id and label required")
	}
	l.Builtin = false
	if _, err := db.ExecContext(ctx,
		`INSERT INTO classification_levels (id, label, rank, builtin, color, created_at) VALUES (?, ?, ?, 0, ?, ?)`,
		l.ID, l.Label, l.Rank, l.Color, nowString()); err != nil {
		return model.ClassificationLevel{}, err
	}
	return l, nil
}

// UpdateClassificationLevel edits a level's label, rank, and color (allowed for built-in and custom alike).
// The id and builtin flag are immutable.
func (db *DB) UpdateClassificationLevel(ctx context.Context, l model.ClassificationLevel) error {
	res, err := db.ExecContext(ctx,
		`UPDATE classification_levels SET label = ?, rank = ?, color = ? WHERE id = ?`,
		l.Label, l.Rank, l.Color, l.ID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteClassificationLevel removes a custom level. Built-ins can't be deleted, and a level still used by
// any connection/model clearance is refused. (Asset tags live in per-project DBs and aren't checked here;
// an orphaned tag fails safe — an unknown tier is blocked by Scale.Allows.)
func (db *DB) DeleteClassificationLevel(ctx context.Context, id string) error {
	var builtin int
	err := db.QueryRowContext(ctx, `SELECT builtin FROM classification_levels WHERE id = ?`, id).Scan(&builtin)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if builtin != 0 {
		return fmt.Errorf("store: built-in classification level %q cannot be deleted", id)
	}
	var used int
	_ = db.QueryRowContext(ctx,
		`SELECT (SELECT COUNT(*) FROM providers WHERE data_clearance = ?) + (SELECT COUNT(*) FROM connection_models WHERE data_clearance = ?)`,
		id, id).Scan(&used)
	if used > 0 {
		return fmt.Errorf("store: classification level %q is in use by %d connection(s)/model(s)", id, used)
	}
	if _, err := db.ExecContext(ctx, `DELETE FROM classification_levels WHERE id = ?`, id); err != nil {
		return err
	}
	return nil
}
