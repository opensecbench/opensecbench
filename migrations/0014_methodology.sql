-- 0014_methodology: per-project methodology adoption + per-item coverage state (ADR-0009). The
-- checklist catalog itself is code (pkg/methodology); only engagement state lives here.

CREATE TABLE project_methodologies (
    project_id     TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    methodology_id TEXT NOT NULL,
    created_at     TEXT NOT NULL,
    PRIMARY KEY (project_id, methodology_id)
);

CREATE TABLE methodology_coverage (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    item_id    TEXT NOT NULL,
    status     TEXT NOT NULL CHECK (status IN ('not_started', 'in_progress', 'covered', 'not_applicable')),
    note       TEXT NOT NULL DEFAULT '',
    updated_at TEXT NOT NULL,
    PRIMARY KEY (project_id, item_id)
);
