-- User-authored report templates (legacy single-database copy of migrations/global/0006). See that file
-- for the rationale; retained here so combined-mode callers/tests (migrations.FS) get the same schema
-- until the two-tier layout (ADR-0049) fully lands.
CREATE TABLE report_templates (
    id         TEXT PRIMARY KEY,
    title      TEXT NOT NULL,
    kind       TEXT NOT NULL,
    base       TEXT NOT NULL DEFAULT '',
    md         TEXT NOT NULL,
    html       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
