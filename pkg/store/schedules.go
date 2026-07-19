package store

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// CreateSchedule adds a playbook schedule. The first run is due now (fires on the next scheduler tick),
// then it repeats every interval.
func (db *DB) CreateSchedule(ctx context.Context, projectID, playbookID string, intervalSeconds int, now time.Time) (model.Schedule, error) {
	if projectID == "" || playbookID == "" || intervalSeconds <= 0 {
		return model.Schedule{}, errors.New("store: schedule needs a project, playbook, and positive interval")
	}
	s := model.Schedule{
		ID: uuid.NewString(), ProjectID: projectID, PlaybookID: playbookID,
		IntervalSeconds: intervalSeconds, Enabled: true, NextRunAt: now, CreatedAt: now,
	}
	if _, err := db.ExecContext(ctx,
		`INSERT INTO schedules (id, project_id, playbook_id, interval_seconds, enabled, last_run_at, next_run_at, created_at)
		 VALUES (?, ?, ?, ?, 1, NULL, ?, ?)`,
		s.ID, projectID, playbookID, intervalSeconds, s.NextRunAt.Format(timeLayout), now.Format(timeLayout)); err != nil {
		return model.Schedule{}, err
	}
	return s, nil
}

func scanSchedule(row interface{ Scan(...any) error }) (model.Schedule, error) {
	var s model.Schedule
	var enabled int
	var last sql.NullString
	var next, created string
	if err := row.Scan(&s.ID, &s.ProjectID, &s.PlaybookID, &s.IntervalSeconds, &enabled, &last, &next, &created); err != nil {
		return model.Schedule{}, err
	}
	s.Enabled = enabled != 0
	if last.Valid && last.String != "" {
		t := parseTime(last.String)
		s.LastRunAt = &t
	}
	s.NextRunAt = parseTime(next)
	s.CreatedAt = parseTime(created)
	return s, nil
}

const scheduleCols = `id, project_id, playbook_id, interval_seconds, enabled, last_run_at, next_run_at, created_at`

// ListSchedulesByProject returns a project's schedules, newest first.
func (db *DB) ListSchedulesByProject(ctx context.Context, projectID string) ([]model.Schedule, error) {
	rows, err := db.QueryContext(ctx, `SELECT `+scheduleCols+` FROM schedules WHERE project_id = ? ORDER BY created_at DESC`, projectID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Schedule
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// ListDueSchedules returns enabled schedules whose next run has passed.
func (db *DB) ListDueSchedules(ctx context.Context, now time.Time) ([]model.Schedule, error) {
	rows, err := db.QueryContext(ctx,
		`SELECT `+scheduleCols+` FROM schedules WHERE enabled = 1 AND next_run_at <= ? ORDER BY next_run_at`, now.Format(timeLayout))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []model.Schedule
	for rows.Next() {
		s, err := scanSchedule(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// MarkScheduleRun records that a schedule fired at lastRun and sets its next run.
func (db *DB) MarkScheduleRun(ctx context.Context, id string, lastRun, nextRun time.Time) error {
	_, err := db.ExecContext(ctx,
		`UPDATE schedules SET last_run_at = ?, next_run_at = ? WHERE id = ?`,
		lastRun.Format(timeLayout), nextRun.Format(timeLayout), id)
	return err
}

// SetScheduleEnabled enables or pauses a schedule.
func (db *DB) SetScheduleEnabled(ctx context.Context, id string, enabled bool) error {
	e := 0
	if enabled {
		e = 1
	}
	res, err := db.ExecContext(ctx, `UPDATE schedules SET enabled = ? WHERE id = ?`, e, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// DeleteSchedule removes a schedule.
func (db *DB) DeleteSchedule(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `DELETE FROM schedules WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
