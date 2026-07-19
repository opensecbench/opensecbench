package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"
)

// SetReachability upserts a project's reachability verdict for a CVE (ADR-0031): whether a reachability
// analyzer proved the vulnerable code is actually called. Later re-runs overwrite the verdict (freshest
// wins). A blank project or CVE is a no-op.
func (db *DB) SetReachability(ctx context.Context, projectID, cve, pkg string, reachable bool, source string) error {
	if projectID == "" || cve == "" {
		return nil
	}
	r := 0
	if reachable {
		r = 1
	}
	now := time.Now().UTC().Format(timeLayout)
	_, err := db.ExecContext(ctx,
		`INSERT INTO reachability (id, project_id, cve, package, reachable, source, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(project_id, cve) DO UPDATE SET
		   package = excluded.package, reachable = excluded.reachable,
		   source = excluded.source, updated_at = excluded.updated_at`,
		uuid.NewString(), projectID, cve, pkg, r, source, now)
	return err
}

// ReachabilityForCVE returns a project's stored reachability verdict for a CVE, and whether one exists. It
// lets an SCA tool without its own reachability analysis (e.g. grype) inherit an analyzer's verdict.
func (db *DB) ReachabilityForCVE(ctx context.Context, projectID, cve string) (reachable bool, known bool) {
	if projectID == "" || cve == "" {
		return false, false
	}
	var r int
	err := db.QueryRowContext(ctx,
		`SELECT reachable FROM reachability WHERE project_id = ? AND cve = ?`, projectID, cve).Scan(&r)
	if errors.Is(err, sql.ErrNoRows) || err != nil {
		return false, false
	}
	return r != 0, true
}
