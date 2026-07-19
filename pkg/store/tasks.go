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
// for asynchronous execution; otherwise it starts running immediately (synchronous run).
type NewTask struct {
	CapabilityID      string
	CapabilityVersion string
	ApplicationID     *string
	AssetID           *string
	ProjectID         *string // scope/project association (network & SCA tasks with no application)
	Actor             string
	Runner            string
	Params            json.RawMessage
	Queued            bool
}

// CreateTask inserts a task. A queued task is pending with no started_at (a worker sets it via
// StartTask); otherwise it starts running immediately with started_at set.
func (db *DB) CreateTask(ctx context.Context, nt NewTask) (model.Task, error) {
	if nt.CapabilityID == "" {
		return model.Task{}, errors.New("store: capability id required")
	}
	params := nt.Params
	if len(params) == 0 {
		params = json.RawMessage(`{}`)
	}
	now := time.Now().UTC()
	nowStr := now.Format(timeLayout)

	t := model.Task{
		ID:                uuid.NewString(),
		CapabilityID:      nt.CapabilityID,
		CapabilityVersion: nt.CapabilityVersion,
		ApplicationID:     nt.ApplicationID,
		AssetID:           nt.AssetID,
		Actor:             nt.Actor,
		Runner:            nt.Runner,
		Params:            params,
		Status:            model.TaskRunning,
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
		 (id, capability_id, capability_version, application_id, asset_id, project_id, actor, runner, params, status, created_at, started_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		t.ID, t.CapabilityID, t.CapabilityVersion, nt.ApplicationID, nt.AssetID, nt.ProjectID, t.Actor, t.Runner,
		string(params), t.Status, nowStr, startedAt)
	if err != nil {
		return model.Task{}, err
	}
	return t, nil
}

// StartTask moves a pending task to running and stamps started_at (a worker claiming the job).
func (db *DB) StartTask(ctx context.Context, id string) error {
	started := time.Now().UTC().Format(timeLayout)
	res, err := db.ExecContext(ctx,
		`UPDATE tasks SET status = ?, started_at = ? WHERE id = ? AND status = ?`,
		model.TaskRunning, started, id, model.TaskPending)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound // already cancelled/started/gone
	}
	return nil
}

// FailUnfinishedTasks marks any tasks left pending or running (e.g. across a control-plane restart) as
// failed with the given message, so they don't linger as ghosts. Returns how many it reconciled.
func (db *DB) FailUnfinishedTasks(ctx context.Context, msg string) (int, error) {
	finished := time.Now().UTC().Format(timeLayout)
	res, err := db.ExecContext(ctx,
		`UPDATE tasks SET status = ?, error = ?, finished_at = ? WHERE status IN (?, ?)`,
		model.TaskFailed, msg, finished, model.TaskPending, model.TaskRunning)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
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
	var t model.Task
	var app, asset sql.NullString
	var params string
	var exit sql.NullInt64
	var created string
	var started, finished sql.NullString
	err := db.QueryRowContext(ctx,
		`SELECT id, capability_id, capability_version, application_id, asset_id, actor, runner,
		        params, status, exit_code, error, created_at, started_at, finished_at
		 FROM tasks WHERE id = ?`, id).
		Scan(&t.ID, &t.CapabilityID, &t.CapabilityVersion, &app, &asset, &t.Actor, &t.Runner,
			&params, &t.Status, &exit, &t.Error, &created, &started, &finished)
	if errors.Is(err, sql.ErrNoRows) {
		return model.Task{}, ErrNotFound
	}
	if err != nil {
		return model.Task{}, err
	}
	t.ApplicationID, t.AssetID = ptr(app), ptr(asset)
	t.Params = json.RawMessage(params)
	if exit.Valid {
		v := int(exit.Int64)
		t.ExitCode = &v
	}
	t.CreatedAt = parseTime(created)
	t.StartedAt = ptrTime(started)
	t.FinishedAt = ptrTime(finished)
	return t, nil
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
		`SELECT id, capability_id, capability_version, application_id, asset_id, actor, runner,
		        params, status, exit_code, error, created_at, started_at, finished_at
		 FROM tasks ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.Task
	for rows.Next() {
		var t model.Task
		var app, asset sql.NullString
		var params string
		var exit sql.NullInt64
		var created string
		var started, finished sql.NullString
		if err := rows.Scan(&t.ID, &t.CapabilityID, &t.CapabilityVersion, &app, &asset, &t.Actor, &t.Runner,
			&params, &t.Status, &exit, &t.Error, &created, &started, &finished); err != nil {
			return nil, err
		}
		t.ApplicationID, t.AssetID = ptr(app), ptr(asset)
		t.Params = json.RawMessage(params)
		if exit.Valid {
			v := int(exit.Int64)
			t.ExitCode = &v
		}
		t.CreatedAt = parseTime(created)
		t.StartedAt = ptrTime(started)
		t.FinishedAt = ptrTime(finished)
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
