package store

import (
	"context"
	"database/sql"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

func scanRoutes(rows *sql.Rows) ([]model.Route, error) {
	var out []model.Route
	for rows.Next() {
		var r model.Route
		var observed int
		var updated string
		if err := rows.Scan(&r.ID, &r.ProjectID, &r.Method, &r.Path, &r.HandlerFile, &r.HandlerLine,
			&r.Framework, &r.Source, &observed, &updated); err != nil {
			return nil, err
		}
		r.Observed = observed != 0
		r.UpdatedAt = parseTime(updated)
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpsertRoute inserts or updates a declared route (ADR-0033), keyed by (project, method, path). It does not
// touch `observed` — traffic reconciliation owns that — so re-extracting from source never clears a route's
// confirmed-exposed status. A blank project or path is a no-op.
func (db *DB) UpsertRoute(ctx context.Context, r model.Route) error {
	if r.ProjectID == "" || r.Path == "" {
		return nil
	}
	obs := 0
	if r.Observed {
		obs = 1
	}
	now := time.Now().UTC().Format(timeLayout)
	_, err := db.ExecContext(ctx,
		`INSERT INTO routes (id, project_id, method, path, handler_file, handler_line, framework, source, observed, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(project_id, method, path) DO UPDATE SET
		   handler_file = excluded.handler_file, handler_line = excluded.handler_line,
		   framework = excluded.framework, source = excluded.source, updated_at = excluded.updated_at`,
		uuid.NewString(), r.ProjectID, r.Method, r.Path, r.HandlerFile, r.HandlerLine, r.Framework, r.Source, obs, now)
	return err
}

// ListRoutesByProject returns a project's routes, newest first.
func (db *DB) ListRoutesByProject(ctx context.Context, projectID string) ([]model.Route, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, method, path, handler_file, handler_line, framework, source, observed, updated_at
		 FROM routes WHERE project_id = ? ORDER BY updated_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRoutes(rows)
}

// RoutesForHandlerFile returns the routes declared in a given source file — used to tie a finding in that
// file to an entry point (ADR-0033). Traffic-only routes (blank handler_file) never match a real file.
func (db *DB) RoutesForHandlerFile(ctx context.Context, projectID, file string) ([]model.Route, error) {
	if file == "" {
		return nil, nil
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, project_id, method, path, handler_file, handler_line, framework, source, observed, updated_at
		 FROM routes WHERE project_id = ? AND handler_file = ? ORDER BY observed DESC, updated_at DESC`,
		projectID, file)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanRoutes(rows)
}

// ReconcileObservedRoutes cross-references a project's routes against captured HTTP traffic (ADR-0033): a
// source-declared route whose path template-matches an observed request path is marked observed=1 (confirmed
// exposed), and a request path with no matching declared route is recorded as a traffic-only route. This is
// what keeps the exposed-route inventory useful in low-information assessments — with only proxy captures
// and no source, the observed endpoints still populate. Best-effort.
func (db *DB) ReconcileObservedRoutes(ctx context.Context, projectID string) error {
	if projectID == "" {
		return nil
	}
	exchanges, err := db.ListExchangesByProject(ctx, projectID)
	if err != nil {
		return err
	}
	routes, err := db.ListRoutesByProject(ctx, projectID)
	if err != nil {
		return err
	}

	// Distinct (method, path) actually seen on the wire.
	type mp struct{ method, path string }
	seen := map[mp]bool{}
	for _, x := range exchanges {
		p := x.URL
		if u, perr := url.Parse(x.URL); perr == nil && u.Path != "" {
			p = u.Path
		}
		if p == "" {
			continue
		}
		seen[mp{strings.ToUpper(x.Method), p}] = true
	}

	for req := range seen {
		matched := false
		for _, r := range routes {
			if (r.Method == "" || strings.EqualFold(r.Method, req.method)) && routeMatchesPath(r.Path, req.path) {
				matched = true
				if !r.Observed {
					if _, uerr := db.ExecContext(ctx, `UPDATE routes SET observed = 1 WHERE id = ?`, r.ID); uerr == nil {
						r.Observed = true
					}
				}
			}
		}
		if !matched {
			// A live endpoint with no declared route — record it as a traffic-only route (no source).
			_ = db.UpsertRoute(ctx, model.Route{
				ProjectID: projectID, Method: req.method, Path: req.path, Source: "traffic", Observed: true,
			})
		}
	}
	return nil
}

// routeMatchesPath reports whether a declared route path template matches a concrete request path. Segments
// must align one-for-one; a route segment that is a parameter ({id} / :id / *) matches any single segment.
func routeMatchesPath(routePath, actualPath string) bool {
	rp := strings.Split(strings.Trim(routePath, "/"), "/")
	ap := strings.Split(strings.Trim(actualPath, "/"), "/")
	if len(rp) != len(ap) {
		return false
	}
	for i := range rp {
		if isRouteParam(rp[i]) {
			continue
		}
		if rp[i] != ap[i] {
			return false
		}
	}
	return true
}

func isRouteParam(seg string) bool {
	if seg == "*" {
		return true
	}
	if strings.HasPrefix(seg, ":") { // :id (Express, gin)
		return true
	}
	if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") { // {id} (Flask, OpenAPI)
		return true
	}
	if strings.HasPrefix(seg, "<") && strings.HasSuffix(seg, ">") { // <int:id> (Flask converters)
		return true
	}
	return false
}
