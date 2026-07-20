-- 0046_project_index: the global directory of projects (ADR-0049). In the two-tier layout the
-- authoritative project row lives in each projects/<id>/project.db; this index lets cross-project listing
-- read one place. Added to the legacy set so the transitional combined-mode Manager (which backs both
-- domains with one database) has it too.
CREATE TABLE project_index (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'active',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
