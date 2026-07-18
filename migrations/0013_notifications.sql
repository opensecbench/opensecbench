-- 0013_notifications: in-app notifications for async, needs-attention events (P8) — approvals
-- waiting, reports ready, and similar. OS-native delivery is a client concern that reads this feed.

CREATE TABLE notifications (
    id         TEXT PRIMARY KEY,
    kind       TEXT NOT NULL,
    title      TEXT NOT NULL,
    body       TEXT NOT NULL DEFAULT '',
    project_id TEXT REFERENCES projects(id) ON DELETE CASCADE,
    link       TEXT NOT NULL DEFAULT '',
    read       INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_notifications_unread ON notifications(read, created_at);
