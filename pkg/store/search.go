package store

import (
	"context"
	"database/sql"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// Search runs omni-search across the entities that exist so far — projects, applications, assets,
// findings, and observations — matching the query as a substring. It extends to more content
// (traffic, docs, KB) as those subsystems land.
func (db *DB) Search(ctx context.Context, q string, perKind int) ([]model.SearchResult, error) {
	if strings.TrimSpace(q) == "" {
		return []model.SearchResult{}, nil
	}
	if perKind <= 0 {
		perKind = 25
	}
	like := "%" + q + "%"
	results := []model.SearchResult{}

	scan := func(rows *sql.Rows, kind string) error {
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			r := model.SearchResult{Kind: kind}
			if err := rows.Scan(&r.ID, &r.Title, &r.Detail); err != nil {
				return err
			}
			results = append(results, r)
		}
		return rows.Err()
	}

	queries := []struct {
		kind string
		sql  string
		args []any
	}{
		{"project", `SELECT id, name, '' FROM projects WHERE name LIKE ? ORDER BY name LIMIT ?`, []any{like, perKind}},
		{"application", `SELECT id, name, project_id FROM applications WHERE name LIKE ? ORDER BY name LIMIT ?`, []any{like, perKind}},
		{"asset", `SELECT id, location, type || ' · ' || sensitivity FROM assets WHERE location LIKE ? ORDER BY created_at LIMIT ?`, []any{like, perKind}},
		{"finding", `SELECT id, title, severity || ' · ' || status FROM findings WHERE title LIKE ? OR description LIKE ? ORDER BY created_at DESC LIMIT ?`, []any{like, like, perKind}},
		{"observation", `SELECT id, title, COALESCE(NULLIF(location, ''), rule_id) FROM observations WHERE title LIKE ? OR rule_id LIKE ? OR location LIKE ? ORDER BY created_at DESC LIMIT ?`, []any{like, like, like, perKind}},
		{"context", `SELECT id, name, type FROM context_items WHERE name LIKE ? ORDER BY created_at DESC LIMIT ?`, []any{like, perKind}},
		{"kb", `SELECT id, title, kind || ' · ' || review_state FROM kb_entries WHERE title LIKE ? OR body LIKE ? OR tags LIKE ? ORDER BY updated_at DESC LIMIT ?`, []any{like, like, like, perKind}},
	}

	for _, qd := range queries {
		rows, err := db.QueryContext(ctx, qd.sql, qd.args...)
		if err != nil {
			return nil, err
		}
		if err := scan(rows, qd.kind); err != nil {
			return nil, err
		}
	}
	return results, nil
}
