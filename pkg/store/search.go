package store

import (
	"context"
	"strings"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// searchQuery is one kind's substring query; each SELECT must project (id, title, detail).
type searchQuery struct {
	kind string
	sql  string
	args []any
}

// Search runs omni-search on this single database — used where every table is present (the combined
// build, and tests). Split deployments use Manager.Search, which sources KB from global.db.
func (db *DB) Search(ctx context.Context, q string, perKind int) ([]model.SearchResult, error) {
	res, err := db.searchEntities(ctx, q, perKind)
	if err != nil {
		return nil, err
	}
	kb, err := db.searchKB(ctx, q, perKind)
	if err != nil {
		return nil, err
	}
	return append(res, kb...), nil
}

// searchEntities matches projects/applications/assets/findings/observations/context in THIS database.
func (db *DB) searchEntities(ctx context.Context, q string, perKind int) ([]model.SearchResult, error) {
	like, ok, pk := searchLike(q, perKind)
	if !ok {
		return []model.SearchResult{}, nil
	}
	return db.runSearch(ctx, []searchQuery{
		{"project", `SELECT id, name, '' FROM projects WHERE name LIKE ? ORDER BY name LIMIT ?`, []any{like, pk}},
		{"application", `SELECT id, name, project_id FROM applications WHERE name LIKE ? ORDER BY name LIMIT ?`, []any{like, pk}},
		{"asset", `SELECT id, location, type || ' · ' || sensitivity FROM assets WHERE location LIKE ? ORDER BY created_at LIMIT ?`, []any{like, pk}},
		{"finding", `SELECT id, title, severity || ' · ' || status FROM findings WHERE title LIKE ? OR description LIKE ? ORDER BY created_at DESC LIMIT ?`, []any{like, like, pk}},
		{"observation", `SELECT id, title, COALESCE(NULLIF(location, ''), rule_id) FROM observations WHERE title LIKE ? OR rule_id LIKE ? OR location LIKE ? ORDER BY created_at DESC LIMIT ?`, []any{like, like, like, pk}},
		{"context", `SELECT id, name, type FROM context_items WHERE name LIKE ? ORDER BY created_at DESC LIMIT ?`, []any{like, pk}},
	})
}

// searchKB matches KB entries in THIS database — global.db under split storage, the single db otherwise.
func (db *DB) searchKB(ctx context.Context, q string, perKind int) ([]model.SearchResult, error) {
	like, ok, pk := searchLike(q, perKind)
	if !ok {
		return []model.SearchResult{}, nil
	}
	return db.runSearch(ctx, []searchQuery{
		{"kb", `SELECT id, title, kind || ' · ' || review_state FROM kb_entries WHERE title LIKE ? OR body LIKE ? OR tags LIKE ? ORDER BY updated_at DESC LIMIT ?`, []any{like, like, like, pk}},
	})
}

// runSearch executes each query and collects results. A query whose table is absent in this database is
// skipped (not fatal), so the same query set is safe against both the combined schema and a split
// project.db that lacks the cross-cutting tables (e.g. kb_entries lives only in global.db).
func (db *DB) runSearch(ctx context.Context, queries []searchQuery) ([]model.SearchResult, error) {
	results := []model.SearchResult{}
	for _, qd := range queries {
		rows, err := db.QueryContext(ctx, qd.sql, qd.args...)
		if err != nil {
			if strings.Contains(err.Error(), "no such table") {
				continue // this kind's table isn't in this database; skip it rather than fail the search
			}
			return nil, err
		}
		for rows.Next() {
			r := model.SearchResult{Kind: qd.kind}
			if err := rows.Scan(&r.ID, &r.Title, &r.Detail); err != nil {
				_ = rows.Close()
				return nil, err
			}
			results = append(results, r)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return nil, err
		}
	}
	return results, nil
}

// searchLike normalizes the query into a LIKE pattern and per-kind limit; ok is false for a blank query.
func searchLike(q string, perKind int) (like string, ok bool, pk int) {
	q = strings.TrimSpace(q)
	if q == "" {
		return "", false, 0
	}
	if perKind <= 0 {
		perKind = 25
	}
	return "%" + q + "%", true, perKind
}

// Search runs the omni-search split-storage-aware: project entities come from the project's database (or
// the global database when projectID is ""), and KB always comes from global.db. This is the entry point
// API handlers should use instead of resolving a single DB, because KB is cross-cutting (ADR-0049).
func (m *Manager) Search(ctx context.Context, projectID, q string, perKind int) ([]model.SearchResult, error) {
	entdb := m.global
	if projectID != "" {
		p, err := m.Project(projectID)
		if err != nil {
			return nil, err
		}
		entdb = p
	}
	res, err := entdb.searchEntities(ctx, q, perKind)
	if err != nil {
		return nil, err
	}
	kb, err := m.global.searchKB(ctx, q, perKind)
	if err != nil {
		return nil, err
	}
	return append(res, kb...), nil
}
