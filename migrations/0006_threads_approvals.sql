-- 0006_threads_approvals: Analyst conversations, messages, and the approval queue.
--
-- A thread is a persisted conversation (forkable). Messages are seq-ordered. An approval is a
-- gated tool call the Analyst paused on, awaiting a human decision — the basis for resumable runs.

CREATE TABLE threads (
    id               TEXT PRIMARY KEY,
    project_id       TEXT REFERENCES projects(id) ON DELETE CASCADE,
    parent_thread_id TEXT REFERENCES threads(id) ON DELETE SET NULL,
    fork_seq         INTEGER, -- parent message seq this thread forked at
    title            TEXT NOT NULL DEFAULT '',
    status           TEXT NOT NULL DEFAULT 'active'
                         CHECK (status IN ('active', 'awaiting_approval', 'done', 'error')),
    provider         TEXT NOT NULL DEFAULT '',
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);
CREATE INDEX idx_threads_project ON threads(project_id);
CREATE INDEX idx_threads_parent ON threads(parent_thread_id);

CREATE TABLE messages (
    id         TEXT PRIMARY KEY,
    thread_id  TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    seq        INTEGER NOT NULL,
    role       TEXT NOT NULL, -- system | user | assistant
    content    TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (thread_id, seq)
);
CREATE INDEX idx_messages_thread ON messages(thread_id);

CREATE TABLE approvals (
    id         TEXT PRIMARY KEY,
    thread_id  TEXT NOT NULL REFERENCES threads(id) ON DELETE CASCADE,
    tool       TEXT NOT NULL,
    args       TEXT NOT NULL DEFAULT '{}',
    status     TEXT NOT NULL DEFAULT 'pending'
                   CHECK (status IN ('pending', 'approved', 'denied')),
    created_at TEXT NOT NULL,
    decided_at TEXT
);
CREATE INDEX idx_approvals_thread ON approvals(thread_id);
CREATE INDEX idx_approvals_status ON approvals(status);
