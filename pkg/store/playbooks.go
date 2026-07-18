package store

import (
	"context"
	"database/sql"
	"errors"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// CreatePlaybookRun starts a playbook run in the running state.
func (db *DB) CreatePlaybookRun(ctx context.Context, playbookID string, assetID *string, actor string) (model.PlaybookRun, error) {
	if playbookID == "" {
		return model.PlaybookRun{}, errors.New("store: playbook id required")
	}
	pr := model.PlaybookRun{ID: uuid.NewString(), PlaybookID: playbookID, AssetID: assetID, Actor: actor, Status: model.PlaybookRunning}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO playbook_runs (id, playbook_id, asset_id, actor, status, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		pr.ID, playbookID, assetID, actor, pr.Status, ts); err != nil {
		return model.PlaybookRun{}, err
	}
	pr.CreatedAt = parseTime(ts)
	return pr, nil
}

// AddRunTask links a task to a playbook run at a step sequence.
func (db *DB) AddRunTask(ctx context.Context, runID, taskID string, seq int) error {
	_, err := db.ExecContext(ctx,
		`INSERT INTO playbook_run_tasks (run_id, task_id, seq) VALUES (?, ?, ?)`, runID, taskID, seq)
	return err
}

// FinishPlaybookRun records a run's terminal status.
func (db *DB) FinishPlaybookRun(ctx context.Context, runID, status string) error {
	res, err := db.ExecContext(ctx,
		`UPDATE playbook_runs SET status = ?, finished_at = ? WHERE id = ?`, status, nowString(), runID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// GetPlaybookRun returns a run with its task ids in order.
func (db *DB) GetPlaybookRun(ctx context.Context, id string) (model.PlaybookRun, error) {
	pr, err := db.scanPlaybookRun(db.QueryRowContext(ctx,
		`SELECT id, playbook_id, asset_id, actor, status, created_at, finished_at FROM playbook_runs WHERE id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return model.PlaybookRun{}, ErrNotFound
	}
	if err != nil {
		return model.PlaybookRun{}, err
	}
	rows, err := db.QueryContext(ctx, `SELECT task_id FROM playbook_run_tasks WHERE run_id = ? ORDER BY seq`, id)
	if err != nil {
		return model.PlaybookRun{}, err
	}
	defer func() { _ = rows.Close() }()
	pr.TaskIDs = []string{}
	for rows.Next() {
		var tid string
		if err := rows.Scan(&tid); err != nil {
			return model.PlaybookRun{}, err
		}
		pr.TaskIDs = append(pr.TaskIDs, tid)
	}
	return pr, rows.Err()
}

// ListPlaybookRuns returns recent runs, newest first (without task ids).
func (db *DB) ListPlaybookRuns(ctx context.Context, limit int) ([]model.PlaybookRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := db.QueryContext(ctx,
		`SELECT id, playbook_id, asset_id, actor, status, created_at, finished_at
		 FROM playbook_runs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.PlaybookRun
	for rows.Next() {
		pr, err := db.scanPlaybookRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, pr)
	}
	return out, rows.Err()
}

func (db *DB) scanPlaybookRun(row interface{ Scan(...any) error }) (model.PlaybookRun, error) {
	var pr model.PlaybookRun
	var asset sql.NullString
	var created string
	var finished sql.NullString
	if err := row.Scan(&pr.ID, &pr.PlaybookID, &asset, &pr.Actor, &pr.Status, &created, &finished); err != nil {
		return model.PlaybookRun{}, err
	}
	pr.AssetID = ptr(asset)
	pr.CreatedAt = parseTime(created)
	pr.FinishedAt = ptrTime(finished)
	return pr, nil
}
