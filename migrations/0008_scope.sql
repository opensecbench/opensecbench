-- 0008_scope: the in-scope target allowlist per project (P6).

CREATE TABLE scope_entries (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('host', 'domain', 'cidr')),
    value      TEXT NOT NULL,
    created_at TEXT NOT NULL
);
CREATE INDEX idx_scope_entries_project ON scope_entries(project_id);
