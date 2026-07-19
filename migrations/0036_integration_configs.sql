-- 0036_integration_configs: persisted, reusable per-project integration configs + inbound-pull support
-- (ADR-0027). Config was previously re-sent in every push request; now it is stored once per (project,
-- integration). credential is a vault secret NAME (the value lives in the vault, never here).

CREATE TABLE integration_configs (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    integration TEXT NOT NULL,          -- connector name: jira | defectdojo
    base_url    TEXT NOT NULL DEFAULT '',
    project_key TEXT NOT NULL DEFAULT '', -- tracker-side scope selector (e.g. a DefectDojo test id)
    credential  TEXT NOT NULL DEFAULT '', -- vault secret NAME (never a value)
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL,
    UNIQUE (project_id, integration)
);

-- Inbound idempotency: a given external finding imports once per (project, integration).
CREATE TABLE integration_imports (
    id             TEXT PRIMARY KEY,
    project_id     TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    integration    TEXT NOT NULL,
    external_id    TEXT NOT NULL,
    observation_id TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    imported_at    TEXT NOT NULL,
    UNIQUE (project_id, integration, external_id)
);

-- Observations previously scoped to a project only through a task; integration-pulled (and analyst
-- thread-origin) observations have no task, so allow a direct project association.
ALTER TABLE observations ADD COLUMN project_id TEXT REFERENCES projects(id);
