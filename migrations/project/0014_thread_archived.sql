-- 0014_thread_archived: soft-archive for Analyst chats (auditability). Archiving retains the thread and its
-- transcript in the project's database — it just drops out of the active list. NULL means active; a timestamp
-- is when it was archived. Kept separate from `status` (a runtime state: active/awaiting_approval/done/error),
-- since a thread of any status can be archived. Permanent deletion is a distinct, deliberate purge.
ALTER TABLE threads ADD COLUMN archived_at TEXT;
CREATE INDEX idx_threads_archived ON threads(archived_at);
