package store

import (
	"context"
	"database/sql"

	"github.com/google/uuid"

	"github.com/opensecbench/opensecbench/pkg/model"
)

// CreateNotification records an in-app notification.
func (db *DB) CreateNotification(ctx context.Context, n model.Notification) (model.Notification, error) {
	if n.ID == "" {
		n.ID = uuid.NewString()
	}
	if n.Kind == "" {
		n.Kind = model.NotifyInfo
	}
	ts := nowString()
	if _, err := db.ExecContext(ctx,
		`INSERT INTO notifications (id, kind, title, body, project_id, link, read, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, 0, ?)`,
		n.ID, n.Kind, n.Title, n.Body, n.ProjectID, n.Link, ts); err != nil {
		return model.Notification{}, err
	}
	n.CreatedAt = parseTime(ts)
	return n, nil
}

// ListNotifications returns notifications newest first; unreadOnly restricts to unread.
func (db *DB) ListNotifications(ctx context.Context, unreadOnly bool, limit int) ([]model.Notification, error) {
	if limit <= 0 {
		limit = 100
	}
	q := `SELECT id, kind, title, body, project_id, link, read, created_at FROM notifications`
	if unreadOnly {
		q += ` WHERE read = 0`
	}
	q += ` ORDER BY created_at DESC LIMIT ?`
	rows, err := db.QueryContext(ctx, q, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var out []model.Notification
	for rows.Next() {
		var n model.Notification
		var project sql.NullString
		var read int
		var created string
		if err := rows.Scan(&n.ID, &n.Kind, &n.Title, &n.Body, &project, &n.Link, &read, &created); err != nil {
			return nil, err
		}
		n.ProjectID = ptr(project)
		n.Read = read != 0
		n.CreatedAt = parseTime(created)
		out = append(out, n)
	}
	return out, rows.Err()
}

// UnreadCount returns the number of unread notifications.
func (db *DB) UnreadCount(ctx context.Context) (int, error) {
	var n int
	err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE read = 0`).Scan(&n)
	return n, err
}

// MarkNotificationRead marks one notification read.
func (db *DB) MarkNotificationRead(ctx context.Context, id string) error {
	res, err := db.ExecContext(ctx, `UPDATE notifications SET read = 1 WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// MarkAllNotificationsRead marks every notification read and returns how many changed.
func (db *DB) MarkAllNotificationsRead(ctx context.Context) (int, error) {
	res, err := db.ExecContext(ctx, `UPDATE notifications SET read = 1 WHERE read = 0`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
