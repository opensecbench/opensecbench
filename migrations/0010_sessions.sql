-- 0010_sessions: interactive terminal sessions run through a runner (ADR-0007, P7). The full
-- transcript is captured to the CAS on close and referenced here for evidence + audit.

CREATE TABLE sessions (
    id                    TEXT PRIMARY KEY,
    project_id            TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind                  TEXT NOT NULL DEFAULT 'terminal' CHECK (kind IN ('terminal')),
    runner                TEXT NOT NULL DEFAULT '',
    container             TEXT NOT NULL DEFAULT '',
    image                 TEXT NOT NULL DEFAULT '',
    status                TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'closed', 'error')),
    actor                 TEXT NOT NULL DEFAULT '',
    transcript_artifact_id TEXT REFERENCES artifacts(id),
    error                 TEXT NOT NULL DEFAULT '',
    created_at            TEXT NOT NULL,
    closed_at             TEXT
);
CREATE INDEX idx_sessions_project ON sessions(project_id, created_at);
