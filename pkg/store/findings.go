package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// --- Observations ---

// CreateObservation inserts an observation. Interpreters and the engine use this to record
// tool/AI-derived results as unreviewed evidence (ADR-0005).
func (db *DB) CreateObservation(ctx context.Context, o model.Observation) (model.Observation, error) {
	if o.Title == "" {
		return model.Observation{}, errors.New("store: observation title required")
	}
	if o.ID == "" {
		o.ID = uuid.NewString()
	}
	if o.Origin == "" {
		o.Origin = model.OriginTool
	}
	if o.ReviewState == "" {
		o.ReviewState = model.ReviewUnreviewed
	}
	if o.Severity == "" {
		o.Severity = "info"
	}
	o.CreatedAt = time.Now().UTC()
	attrs := ""
	if len(o.Attributes) > 0 {
		b, _ := json.Marshal(o.Attributes)
		attrs = string(b)
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO observations
		 (id, task_id, artifact_id, project_id, origin, review_state, title, detail, severity, rule_id, location, attributes, fingerprint, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		o.ID, o.TaskID, o.ArtifactID, o.ProjectID, o.Origin, o.ReviewState, o.Title, o.Detail, o.Severity,
		o.RuleID, o.Location, attrs, o.Fingerprint, o.CreatedAt.Format(timeLayout))
	if err != nil {
		return model.Observation{}, err
	}
	return o, nil
}

// ListObservationsByTask returns a task's observations, oldest first.
func (db *DB) ListObservationsByTask(ctx context.Context, taskID string) ([]model.Observation, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, task_id, artifact_id, project_id, origin, review_state, title, detail, severity, rule_id, location, attributes, fingerprint, created_at
		 FROM observations WHERE task_id = ? ORDER BY created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	return scanObservations(rows)
}

// GetObservation returns one observation by id.
func (db *DB) GetObservation(ctx context.Context, id string) (model.Observation, error) {
	var o model.Observation
	var task, artifact, project sql.NullString
	var attrs, created string
	err := db.QueryRowContext(ctx,
		`SELECT id, task_id, artifact_id, project_id, origin, review_state, title, detail, severity, rule_id, location, attributes, fingerprint, created_at
		 FROM observations WHERE id = ?`, id).
		Scan(&o.ID, &task, &artifact, &project, &o.Origin, &o.ReviewState, &o.Title, &o.Detail,
			&o.Severity, &o.RuleID, &o.Location, &attrs, &o.Fingerprint, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Observation{}, ErrNotFound
	}
	if err != nil {
		return model.Observation{}, err
	}
	o.TaskID, o.ArtifactID, o.ProjectID = ptr(task), ptr(artifact), ptr(project)
	o.Attributes = parseAttrs(attrs)
	o.CreatedAt = parseTime(created)
	return o, nil
}

// RefreshObservation updates a re-seen observation's mutable, interpreter-derived fields — severity, detail,
// and attributes — without disturbing its review_state or id (ADR-0037). This keeps a deduped finding's
// data current across re-scans (e.g. a corrected severity or a changed reachability/exposure signal) while
// preserving human triage; the caller does not re-run dispositions.
func (db *DB) RefreshObservation(ctx context.Context, id, severity, detail string, attrs map[string]string) error {
	a := ""
	if len(attrs) > 0 {
		b, _ := json.Marshal(attrs)
		a = string(b)
	}
	_, err := db.ExecContext(ctx,
		`UPDATE observations SET severity = ?, detail = ?, attributes = ? WHERE id = ?`,
		severity, detail, a, id)
	return err
}

// ObservationByFingerprint returns the id of an existing observation with the same content fingerprint in
// the project, if any. The engine uses it to dedup re-scans so the same finding is not re-created or
// re-dispositioned (ADR-0029). An empty fingerprint or project never matches.
func (db *DB) ObservationByFingerprint(ctx context.Context, projectID, fingerprint string) (string, bool) {
	if projectID == "" || fingerprint == "" {
		return "", false
	}
	var id string
	err := db.QueryRowContext(ctx,
		`SELECT id FROM observations WHERE project_id = ? AND fingerprint = ? LIMIT 1`,
		projectID, fingerprint).Scan(&id)
	if err != nil {
		return "", false
	}
	return id, true
}

// ObservationForVuln returns the id of the observation that already owns any of the given advisory ids
// (CVE/GHSA) in the project — used to merge the same vulnerability reported by a second tool into the
// first observation instead of creating a duplicate (ADR-0037). Mirrors InvestigationForVuln.
func (db *DB) ObservationForVuln(ctx context.Context, projectID string, vulnIDs []string) (string, bool) {
	if projectID == "" || len(vulnIDs) == 0 {
		return "", false
	}
	q := `SELECT observation_id FROM observation_vulns WHERE project_id = ? AND vuln_id IN (?` +
		strings.Repeat(", ?", len(vulnIDs)-1) + `) LIMIT 1`
	args := make([]any, 0, len(vulnIDs)+1)
	args = append(args, projectID)
	for _, v := range vulnIDs {
		args = append(args, v)
	}
	var id string
	if err := db.QueryRowContext(ctx, q, args...).Scan(&id); err != nil {
		return "", false
	}
	return id, true
}

// RecordObservationVulns claims each advisory id for an observation. Idempotent: an id already claimed
// (by this or another observation) is left as-is, so the first observation owns the vuln (ADR-0037).
func (db *DB) RecordObservationVulns(ctx context.Context, projectID, observationID string, vulnIDs []string) error {
	now := time.Now().UTC().Format(timeLayout)
	for _, v := range vulnIDs {
		if v == "" {
			continue
		}
		if _, err := db.ExecContext(ctx,
			`INSERT OR IGNORE INTO observation_vulns (id, observation_id, project_id, vuln_id, created_at)
			 VALUES (?, ?, ?, ?, ?)`,
			uuid.NewString(), observationID, projectID, v, now); err != nil {
			return err
		}
	}
	return nil
}

func parseAttrs(s string) map[string]string {
	if s == "" {
		return nil
	}
	var m map[string]string
	_ = json.Unmarshal([]byte(s), &m)
	return m
}

func scanObservations(rows *sql.Rows) ([]model.Observation, error) {
	var out []model.Observation
	for rows.Next() {
		var o model.Observation
		var task, artifact, project sql.NullString
		var attrs, created string
		if err := rows.Scan(&o.ID, &task, &artifact, &project, &o.Origin, &o.ReviewState, &o.Title, &o.Detail,
			&o.Severity, &o.RuleID, &o.Location, &attrs, &o.Fingerprint, &created); err != nil {
			return nil, err
		}
		o.TaskID, o.ArtifactID, o.ProjectID = ptr(task), ptr(artifact), ptr(project)
		o.Attributes = parseAttrs(attrs)
		o.CreatedAt = parseTime(created)
		out = append(out, o)
	}
	return out, rows.Err()
}

// ReviewObservation sets an observation's review state (confirmed | rejected | unreviewed).
func (db *DB) ReviewObservation(ctx context.Context, id, state string) error {
	switch state {
	case model.ReviewConfirmed, model.ReviewRejected, model.ReviewUnreviewed:
	default:
		return fmt.Errorf("store: invalid review state %q", state)
	}
	res, err := db.ExecContext(ctx, `UPDATE observations SET review_state = ? WHERE id = ?`, state, id)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// TriageObservation records an agent (or human) triage disposition on an observation: it sets the review
// state and merges triage metadata — a rationale and the actor — into the observation's attributes so the
// decision is auditable and visible in the UI. `dismiss` marks it rejected; `flag` leaves it unreviewed but
// tags it for human attention. Both are reversible (restore to unreviewed clears nothing but the state).
func (db *DB) TriageObservation(ctx context.Context, id, disposition, rationale, actor string) error {
	o, err := db.GetObservation(ctx, id)
	if err != nil {
		return err
	}
	attrs := o.Attributes
	if attrs == nil {
		attrs = map[string]string{}
	}
	attrs["triage_rationale"] = rationale
	attrs["triaged_by"] = actor
	state := model.ReviewUnreviewed
	switch disposition {
	case "dismiss":
		state = model.ReviewRejected
		delete(attrs, "triage_flag")
	case "flag":
		attrs["triage_flag"] = "true" // needs a human look
	default:
		return fmt.Errorf("store: invalid triage disposition %q (want dismiss|flag)", disposition)
	}
	b, _ := json.Marshal(attrs)
	res, err := db.ExecContext(ctx, `UPDATE observations SET review_state = ?, attributes = ? WHERE id = ?`, state, string(b), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- Findings ---

// NewFinding is the input for creating a finding from confirmed observations.
type NewFinding struct {
	ApplicationID  *string
	Title          string
	Severity       string
	Description    string
	CWE            string
	ObservationIDs []string
}

// CreateFinding assembles a finding from observations. Per ADR-0005, every supporting observation
// must already be confirmed; otherwise the finding is rejected.
func (db *DB) CreateFinding(ctx context.Context, nf NewFinding) (model.Finding, error) {
	if nf.Title == "" {
		return model.Finding{}, errors.New("store: finding title required")
	}
	severity := nf.Severity
	if severity == "" {
		severity = "medium"
	}
	now := time.Now().UTC()
	nowStr := now.Format(timeLayout)
	f := model.Finding{
		ID:             uuid.NewString(),
		ApplicationID:  nf.ApplicationID,
		Title:          nf.Title,
		Severity:       severity,
		Status:         model.FindingOpen,
		Description:    nf.Description,
		CWE:            nf.CWE,
		ObservationIDs: nf.ObservationIDs,
		CreatedAt:      now,
		UpdatedAt:      now,
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return model.Finding{}, err
	}
	defer func() { _ = tx.Rollback() }()

	for _, oid := range nf.ObservationIDs {
		var state string
		err := tx.QueryRowContext(ctx, `SELECT review_state FROM observations WHERE id = ?`, oid).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) {
			return model.Finding{}, fmt.Errorf("store: observation %s not found", oid)
		}
		if err != nil {
			return model.Finding{}, err
		}
		if state != model.ReviewConfirmed {
			return model.Finding{}, fmt.Errorf("store: observation %s is %s, must be confirmed to support a finding", oid, state)
		}
	}

	if _, err := tx.ExecContext(ctx,
		`INSERT INTO findings (id, application_id, title, severity, status, description, cwe, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.ID, nf.ApplicationID, f.Title, f.Severity, f.Status, f.Description, f.CWE, nowStr, nowStr); err != nil {
		return model.Finding{}, err
	}
	for _, oid := range nf.ObservationIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO finding_observations (finding_id, observation_id) VALUES (?, ?)`, f.ID, oid); err != nil {
			return model.Finding{}, err
		}
	}
	if err := tx.Commit(); err != nil {
		return model.Finding{}, err
	}
	return f, nil
}

// GetFinding returns a finding with its supporting observation ids.
func (db *DB) GetFinding(ctx context.Context, id string) (model.Finding, error) {
	var f model.Finding
	var app sql.NullString
	var created, updated string
	err := db.QueryRowContext(ctx,
		`SELECT id, application_id, title, severity, status, description, cwe, created_at, updated_at
		 FROM findings WHERE id = ?`, id).
		Scan(&f.ID, &app, &f.Title, &f.Severity, &f.Status, &f.Description, &f.CWE, &created, &updated)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Finding{}, ErrNotFound
	}
	if err != nil {
		return model.Finding{}, err
	}
	f.ApplicationID = ptr(app)
	f.CreatedAt, f.UpdatedAt = parseTime(created), parseTime(updated)

	rows, err := db.QueryContext(ctx,
		`SELECT observation_id FROM finding_observations WHERE finding_id = ? ORDER BY observation_id`, id)
	if err != nil {
		return model.Finding{}, err
	}
	defer func() { _ = rows.Close() }()
	f.ObservationIDs = []string{}
	for rows.Next() {
		var oid string
		if err := rows.Scan(&oid); err != nil {
			return model.Finding{}, err
		}
		f.ObservationIDs = append(f.ObservationIDs, oid)
	}
	return f, rows.Err()
}

// SetFindingStatus updates a finding's status (open | confirmed | remediated | accepted |
// false_positive).
func (db *DB) SetFindingStatus(ctx context.Context, id, status string) error {
	res, err := db.ExecContext(ctx,
		`UPDATE findings SET status = ?, updated_at = ? WHERE id = ?`, status, nowString(), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ListFindings returns all findings, newest first (without supporting-observation ids).
func (db *DB) ListFindings(ctx context.Context) ([]model.Finding, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, application_id, title, severity, status, description, cwe, created_at, updated_at
		 FROM findings ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.Finding
	idx := map[string]int{}
	for rows.Next() {
		var f model.Finding
		var app sql.NullString
		var created, updated string
		if err := rows.Scan(&f.ID, &app, &f.Title, &f.Severity, &f.Status, &f.Description, &f.CWE, &created, &updated); err != nil {
			return nil, err
		}
		f.ApplicationID = ptr(app)
		f.CreatedAt, f.UpdatedAt = parseTime(created), parseTime(updated)
		f.ObservationIDs = []string{} // never nil, so the JSON is [] not null (the UI maps over it)
		idx[f.ID] = len(out)
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	_ = rows.Close() // done reading findings; free the connection before the next query

	// Attach supporting observations in one pass (findings carry their location via these, ADR-0050).
	orows, err := db.QueryContext(ctx,
		`SELECT finding_id, observation_id FROM finding_observations ORDER BY finding_id, observation_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = orows.Close() }()
	for orows.Next() {
		var fid, oid string
		if err := orows.Scan(&fid, &oid); err != nil {
			return nil, err
		}
		if i, ok := idx[fid]; ok {
			out[i].ObservationIDs = append(out[i].ObservationIDs, oid)
		}
	}
	return out, orows.Err()
}

// FindingCount is a per-project findings tally.
type FindingCount struct {
	Total int `json:"total"`
	High  int `json:"high"` // high + critical
	Open  int `json:"open"` // status='open' — awaiting a human disposition decision
}

// FindingCountsByProject returns, per project id, the total findings, the high/critical count, and the
// count still in 'open' status (awaiting review) — via the application → project join. Findings not
// attached to an application are not attributed to a project.
func (db *DB) FindingCountsByProject(ctx context.Context) (map[string]FindingCount, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT a.project_id,
		        COUNT(*),
		        SUM(CASE WHEN f.severity IN ('critical','high') THEN 1 ELSE 0 END),
		        SUM(CASE WHEN f.status = 'open' THEN 1 ELSE 0 END)
		 FROM findings f JOIN applications a ON f.application_id = a.id
		 GROUP BY a.project_id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]FindingCount{}
	for rows.Next() {
		var pid string
		var total, high, open int
		if err := rows.Scan(&pid, &total, &high, &open); err != nil {
			return nil, err
		}
		out[pid] = FindingCount{Total: total, High: high, Open: open}
	}
	return out, rows.Err()
}
