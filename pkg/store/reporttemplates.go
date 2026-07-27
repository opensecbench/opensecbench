package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// SaveReportTemplate upserts a user-authored report template by id. Callers supply a stable id (e.g.
// forked from a built-in as "executive-custom"); saving the same id again edits it in place, keeping
// created_at. Built-in templates have no row here, which is how they stay immutable.
func (db *DB) SaveReportTemplate(ctx context.Context, t model.ReportTemplate) (model.ReportTemplate, error) {
	if t.ID == "" || t.Title == "" || t.MD == "" || t.HTML == "" {
		return model.ReportTemplate{}, errors.New("store: report template needs an id, title, md, and html")
	}
	ts := nowString()
	// created_at is preserved on edit via COALESCE against the existing row; updated_at always bumps.
	if _, err := db.ExecContext(ctx,
		`INSERT INTO report_templates (id, title, kind, base, md, html, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(id) DO UPDATE SET
		   title = excluded.title, kind = excluded.kind, base = excluded.base,
		   md = excluded.md, html = excluded.html, updated_at = excluded.updated_at`,
		t.ID, t.Title, t.Kind, t.Base, t.MD, t.HTML, ts, ts); err != nil {
		return model.ReportTemplate{}, err
	}
	return db.GetReportTemplate(ctx, t.ID)
}

// UpdateReportTemplate edits an existing saved template in place, returning ErrNotFound if no row exists
// (built-ins have no row, so editing one this way 404s — the caller forks a copy instead).
func (db *DB) UpdateReportTemplate(ctx context.Context, t model.ReportTemplate) (model.ReportTemplate, error) {
	if t.ID == "" || t.Title == "" || t.MD == "" || t.HTML == "" {
		return model.ReportTemplate{}, errors.New("store: report template needs an id, title, md, and html")
	}
	ts := nowString()
	res, err := db.ExecContext(ctx,
		`UPDATE report_templates SET title = ?, kind = ?, base = ?, md = ?, html = ?, updated_at = ? WHERE id = ?`,
		t.Title, t.Kind, t.Base, t.MD, t.HTML, ts, t.ID)
	if err != nil {
		return model.ReportTemplate{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.ReportTemplate{}, ErrNotFound
	}
	return db.GetReportTemplate(ctx, t.ID)
}

// GetReportTemplate returns one saved report template by id.
func (db *DB) GetReportTemplate(ctx context.Context, id string) (model.ReportTemplate, error) {
	var t model.ReportTemplate
	var created, updated string
	err := db.QueryRowContext(ctx,
		`SELECT id, title, kind, base, md, html, created_at, updated_at FROM report_templates WHERE id = ?`, id).
		Scan(&t.ID, &t.Title, &t.Kind, &t.Base, &t.MD, &t.HTML, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return model.ReportTemplate{}, ErrNotFound
	}
	if err != nil {
		return model.ReportTemplate{}, err
	}
	t.CreatedAt = parseTime(created)
	t.UpdatedAt = parseTime(updated)
	return t, nil
}

// ListReportTemplates returns all saved report templates, newest first.
func (db *DB) ListReportTemplates(ctx context.Context) ([]model.ReportTemplate, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, title, kind, base, md, html, created_at, updated_at FROM report_templates ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.ReportTemplate
	for rows.Next() {
		var t model.ReportTemplate
		var created, updated string
		if err := rows.Scan(&t.ID, &t.Title, &t.Kind, &t.Base, &t.MD, &t.HTML, &created, &updated); err != nil {
			return nil, err
		}
		t.CreatedAt = parseTime(created)
		t.UpdatedAt = parseTime(updated)
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteReportTemplate removes a user-saved report template. Built-ins have no row (ErrNotFound).
func (db *DB) DeleteReportTemplate(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM report_templates WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
