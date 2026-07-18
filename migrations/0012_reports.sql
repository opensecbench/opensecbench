-- 0012_reports: generated engagement reports (ADR-0008). The rendered bytes live in the CAS as an
-- artifact; this row records provenance (template, format, project) and makes them re-downloadable.

CREATE TABLE reports (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    template_id TEXT NOT NULL,
    format      TEXT NOT NULL,
    title       TEXT NOT NULL DEFAULT '',
    artifact_id TEXT NOT NULL REFERENCES artifacts(id),
    created_at  TEXT NOT NULL
);
CREATE INDEX idx_reports_project ON reports(project_id, created_at);
