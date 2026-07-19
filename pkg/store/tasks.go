package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// NewTask is the input for starting a task. Queued creates it in the pending state (started_at unset)
// for asynchronous execution; otherwise it starts running immediately (synchronous run). SecretRefs
// (envVar -> vault secret NAME) and TargetDir are persisted so a queued task can be reconstructed and
// re-run after a restart (ADR-0023); resolved secret values are never stored (ADR-0011).
type NewTask struct {
	CapabilityID      string
	CapabilityVersion string
	ApplicationID     *string
	AssetID           *string
	ProjectID         *string // scope/project association (network & SCA tasks with no application)
	Actor             string
	Runner            string
	Params            json.RawMessage
	SecretRefs        map[string]string
	TargetDir         string
	RunnerTarget      string // '' = local runner; otherwise a runners.id (ADR-0024)
	Queued            bool
}

// taskCols is the full task column list, shared by every task read so reconstruction data (project_id,
// secret_refs, target_dir, attempts) is always loaded.
const taskCols = `id, capability_id, capability_version, application_id, asset_id, project_id, actor, runner,
	params, status, exit_code, error, attempts, created_at, started_at, finished_at, secret_refs, target_dir, runner_target`

// scanTask reads one task row selected with taskCols.
func scanTask(s interface{ Scan(...any) error }) (model.Task, error) {
	var t model.Task
	var app, asset, project sql.NullString
	var params, secretRefs, targetDir, runnerTarget string
	var exit sql.NullInt64
	var created string
	var started, finished sql.NullString
	if err := s.Scan(&t.ID, &t.CapabilityID, &t.CapabilityVersion, &app, &asset, &project, &t.Actor, &t.Runner,
		&params, &t.Status, &exit, &t.Error, &t.Attempts, &created, &started, &finished, &secretRefs, &targetDir, &runnerTarget); err != nil {
		return model.Task{}, err
	}
	t.ApplicationID, t.AssetID, t.ProjectID = ptr(app), ptr(asset), ptr(project)
	t.Params = json.RawMessage(params)
	if secretRefs != "" {
		_ = json.Unmarshal([]byte(secretRefs), &t.SecretRefs)
	}
	t.TargetDir = targetDir
	t.RunnerTarget = runnerTarget
	if exit.Valid {
		v := int(exit.Int64)
		t.ExitCode = &v
	}
	t.CreatedAt = parseTime(created)
	t.StartedAt = ptrTime(started)
	t.FinishedAt = ptrTime(finished)
	return t, nil
}

// CreateTask inserts a task. A queued task is pending with no started_at (a worker claims it via
// ClaimNextPendingTask); otherwise it starts running immediately with started_at set.
func (db *DB) CreateTask(ctx context.Context, nt NewTask) (model.Task, error) {
	if nt.CapabilityID == "" {
		return model.Task{}, errors.New("store: capability id required")
	}
	params := nt.Params
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	secretRefs := ""
	if len(nt.SecretRefs) > 0 {
		b, _ := json.Marshal(nt.SecretRefs)
		secretRefs = string(b)
	}
	now := time.Now().UTC()
	nowStr := now.Format(timeLayout)

	t := model.Task{
		ID:                uuid.NewString(),
		CapabilityID:      nt.CapabilityID,
		CapabilityVersion: nt.CapabilityVersion,
		ApplicationID:     nt.ApplicationID,
		AssetID:           nt.AssetID,
		ProjectID:         nt.ProjectID,
		Actor:             nt.Actor,
		Runner:            nt.Runner,
		Params:            params,
		Status:            model.TaskRunning,
		SecretRefs:        nt.SecretRefs,
		TargetDir:         nt.TargetDir,
		RunnerTarget:      nt.RunnerTarget,
		CreatedAt:         now,
		StartedAt:         &now,
	}
	var startedAt any = nowStr
	if nt.Queued {
		t.Status = model.TaskPending
		t.StartedAt = nil
		startedAt = nil
	}
	_, err := db.ExecContext(ctx,
		`INSERT INTO tasks
		 (id, capability_id, capability_version, application_id, asset_id, project_id, actor, runner, params, status, created_at, started_at, secret_refs, target_dir, runner_target)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.CapabilityID, t.CapabilityVersion, nt.ApplicationID, nt.AssetID, nt.ProjectID, t.Actor, t.Runner,
		string(params), t.Status, nowStr, startedAt, secretRefs, nt.TargetDir, nt.RunnerTarget)
	if err != nil {
		return model.Task{}, err
	}
	return t, nil
}

// ClaimNextPendingTask atomically claims the oldest pending task for a worker: it flips exactly one
// pending row to running, stamps started_at, and bumps attempts, returning the claimed task. ok is false
// when the queue is empty (or the chosen row was claimed by a concurrent worker — the caller retries).
func (db *DB) ClaimNextPendingTask(ctx context.Context) (model.Task, bool, error) {
	started := time.Now().UTC().Format(timeLayout)
	// One atomic statement: the subselect picks the oldest pending id and the guarded WHERE ensures only
	// one worker wins it. RETURNING gives back the full row so the worker can reconstruct the run.
	t, err := scanTask(db.QueryRowContext(ctx,
		`UPDATE tasks SET status = ?, started_at = ?, attempts = attempts + 1
		 WHERE id = (SELECT id FROM tasks WHERE status = ? ORDER BY created_at LIMIT 1)
		   AND status = ?
		 RETURNING `+taskCols,
		model.TaskRunning, started, model.TaskPending, model.TaskPending))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Task{}, false, nil
	}
	if err != nil {
		return model.Task{}, false, err
	}
	return t, true, nil
}

// RequeueInterruptedTasks resets tasks left running by a prior process (a crash mid-run) back to pending
// so the worker pool resumes them (ADR-0023). attempts is intentionally preserved so a task that keeps
// crashing the process is eventually capped. Returns how many were requeued.
func (db *DB) RequeueInterruptedTasks(ctx context.Context) (int, error) {
	res, err := db.ExecContext(ctx,
		`UPDATE tasks SET status = ?, started_at = NULL WHERE status = ?`,
		model.TaskPending, model.TaskRunning)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// CancelPendingTask marks a still-queued task failed ("cancelled by user"), guarded so it only affects a
// pending row (a running task is cancelled by killing its container instead). Returns whether it matched.
func (db *DB) CancelPendingTask(ctx context.Context, id string) (bool, error) {
	finished := time.Now().UTC().Format(timeLayout)
	res, err := db.ExecContext(ctx,
		`UPDATE tasks SET status = ?, error = ?, finished_at = ? WHERE id = ? AND status = ?`,
		model.TaskFailed, "cancelled by user", finished, id, model.TaskPending)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	return n == 1, nil
}

// FinishTask records a terminal status, exit code, and error for a task.
func (db *DB) FinishTask(ctx context.Context, id, status string, exitCode *int, errMsg string) error {
	finished := time.Now().UTC().Format(timeLayout)
	res, err := db.ExecContext(ctx,
		`UPDATE tasks SET status = ?, exit_code = ?, error = ?, finished_at = ? WHERE id = ?`,
		status, exitCode, errMsg, finished, id)
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

// GetTask returns a task by id.
func (db *DB) GetTask(ctx context.Context, id string) (model.Task, error) {
	t, err := scanTask(db.QueryRowContext(ctx, `SELECT `+taskCols+` FROM tasks WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.Task{}, ErrNotFound
	}
	return t, err
}

// CreateArtifact records an artifact (its bytes must already be stored in the CAS).
func (db *DB) CreateArtifact(ctx context.Context, a model.Artifact) (model.Artifact, error) {
	if a.SHA256 == "" {
		return model.Artifact{}, errors.New("store: artifact sha256 required")
	}
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.MediaType == "" {
		a.MediaType = "application/octet-stream"
	}
	a.CreatedAt = time.Now().UTC()
	_, err := db.ExecContext(ctx,
		`INSERT INTO artifacts (id, task_id, sha256, media_type, size, kind, name, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		a.ID, a.TaskID, a.SHA256, a.MediaType, a.Size, a.Kind, a.Name, a.CreatedAt.Format(timeLayout))
	if err != nil {
		return model.Artifact{}, err
	}
	return a, nil
}

// ListTasks returns recent tasks, newest first.
func (db *DB) ListTasks(ctx context.Context, limit int) ([]model.Task, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx,
		`SELECT `+taskCols+` FROM tasks ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.Task
	for rows.Next() {
		t, err := scanTask(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// GetArtifact returns an artifact by id.
func (db *DB) GetArtifact(ctx context.Context, id string) (model.Artifact, error) {
	var a model.Artifact
	var task sql.NullString
	var created string
	err := db.QueryRowContext(ctx,
		`SELECT id, task_id, sha256, media_type, size, kind, name, created_at
		 FROM artifacts WHERE id = ?`, id).
		Scan(&a.ID, &task, &a.SHA256, &a.MediaType, &a.Size, &a.Kind, &a.Name, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Artifact{}, ErrNotFound
	}
	if err != nil {
		return model.Artifact{}, err
	}
	a.TaskID = ptr(task)
	a.CreatedAt = parseTime(created)
	return a, nil
}

// ListArtifactsByTask returns a task's artifacts, oldest first.
func (db *DB) ListArtifactsByTask(ctx context.Context, taskID string) ([]model.Artifact, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT id, task_id, sha256, media_type, size, kind, name, created_at
		 FROM artifacts WHERE task_id = ? ORDER BY created_at`, taskID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.Artifact
	for rows.Next() {
		var a model.Artifact
		var task sql.NullString
		var created string
		if err := rows.Scan(&a.ID, &task, &a.SHA256, &a.MediaType, &a.Size, &a.Kind, &a.Name, &created); err != nil {
			return nil, err
		}
		a.TaskID = ptr(task)
		a.CreatedAt = parseTime(created)
		out = append(out, a)
	}
	return out, rows.Err()
}

func ptrTime(ns sql.NullString) *time.Time {
	if !ns.Valid {
		return nil
	}
	t := parseTime(ns.String)
	return &t
}
