package store

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// AddReachabilityFact records (or replaces) one source's reachability determination for a subject. Each
// source has at most one current fact per subject (UNIQUE), so a re-run overwrites its own verdict while
// other sources' facts stand — that's the aggregation. A blank project/subject/source is a no-op.
func (db *DB) AddReachabilityFact(ctx context.Context, f model.ReachabilityFact) error {
	if f.ProjectID == "" || f.SubjectType == "" || f.SubjectKey == "" || f.Source == "" {
		return nil
	}
	if f.Reachable == "" {
		f.Reachable = model.ReachUnknown
	}
	if f.Confidence == "" {
		f.Confidence = model.ReachConfMedium
	}
	now := time.Now().UTC().Format(timeLayout)
	_, err := db.ExecContext(ctx,
		`INSERT INTO reachability_facts
		   (id, project_id, subject_type, subject_key, reachable, confidence, source, method, rationale, actor, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT(project_id, subject_type, subject_key, source) DO UPDATE SET
		   reachable = excluded.reachable, confidence = excluded.confidence,
		   method = excluded.method, rationale = excluded.rationale, actor = excluded.actor,
		   updated_at = excluded.updated_at`,
		uuid.NewString(), f.ProjectID, f.SubjectType, f.SubjectKey, f.Reachable, f.Confidence,
		f.Source, f.Method, f.Rationale, f.Actor, now, now)
	return err
}

const reachFactCols = `id, project_id, subject_type, subject_key, reachable, confidence, source, method, rationale, actor, created_at, updated_at`

func scanReachFacts(rows interface {
	Next() bool
	Scan(...any) error
	Err() error
}) ([]model.ReachabilityFact, error) {
	var out []model.ReachabilityFact
	for rows.Next() {
		var f model.ReachabilityFact
		var created, updated string
		if err := rows.Scan(&f.ID, &f.ProjectID, &f.SubjectType, &f.SubjectKey, &f.Reachable, &f.Confidence,
			&f.Source, &f.Method, &f.Rationale, &f.Actor, &created, &updated); err != nil {
			return nil, err
		}
		f.CreatedAt, f.UpdatedAt = parseTime(created), parseTime(updated)
		out = append(out, f)
	}
	return out, rows.Err()
}

// ReachabilityFacts returns every source's fact for one subject, newest first.
func (db *DB) ReachabilityFacts(ctx context.Context, projectID, subjectType, subjectKey string) ([]model.ReachabilityFact, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+reachFactCols+` FROM reachability_facts
		 WHERE project_id = ? AND subject_type = ? AND subject_key = ? ORDER BY updated_at DESC`,
		projectID, subjectType, subjectKey)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanReachFacts(rows)
}

// ListReachabilityFactsByProject returns all of a project's reachability facts (for the surface/UI).
func (db *DB) ListReachabilityFactsByProject(ctx context.Context, projectID string) ([]model.ReachabilityFact, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+reachFactCols+` FROM reachability_facts WHERE project_id = ? ORDER BY updated_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanReachFacts(rows)
}

var reachConfidenceRank = map[string]int{
	model.ReachConfLow: 1, model.ReachConfMedium: 2, model.ReachConfHigh: 3, model.ReachConfProven: 4,
}

// ResolveReachability aggregates a subject's facts into an effective verdict: the highest-confidence fact
// wins, and on a tie "reachable" beats "unreachable" (conservative — never hide a reachable path). Returns
// the verdict, the winning confidence, and all contributing facts. No facts → (unknown, "", nil).
func (db *DB) ResolveReachability(ctx context.Context, projectID, subjectType, subjectKey string) (verdict, confidence string, facts []model.ReachabilityFact) {
	facts, _ = db.ReachabilityFacts(ctx, projectID, subjectType, subjectKey)
	if len(facts) == 0 {
		return model.ReachUnknown, "", nil
	}
	best := -1
	verdict = model.ReachUnknown
	for _, f := range facts {
		rank := reachConfidenceRank[f.Confidence]
		if rank > best || (rank == best && f.Reachable == model.ReachReachable && verdict != model.ReachReachable) {
			best = rank
			verdict, confidence = f.Reachable, f.Confidence
		}
	}
	return verdict, confidence, facts
}
