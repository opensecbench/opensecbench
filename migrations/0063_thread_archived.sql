-- 0063_thread_archived: soft-archive for Analyst chats (legacy set mirror of project/0014). NULL = active;
-- a timestamp is when it was archived. Archived threads are retained for auditability but hidden from the
-- active list. Separate from `status` (a runtime state), since a thread of any status can be archived.
ALTER TABLE threads ADD COLUMN archived_at TEXT;
CREATE INDEX idx_threads_archived ON threads(archived_at);
