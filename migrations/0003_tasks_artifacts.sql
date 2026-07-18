-- 0003_tasks_artifacts: execution + evidence roots (ADR-0002, ADR-0004).
--
-- A task is one capability invocation; artifacts are its immutable outputs, stored in the CAS
-- and addressed by sha256. Provenance flows artifact -> task -> (capability+version, runner).
-- Capability id/version are denormalized onto the task so provenance survives capability changes.

CREATE TABLE tasks (
    id                 TEXT PRIMARY KEY,
    capability_id      TEXT NOT NULL,
    capability_version TEXT NOT NULL,
    application_id     TEXT REFERENCES applications(id) ON DELETE SET NULL,
    asset_id           TEXT REFERENCES assets(id) ON DELETE SET NULL,
    actor              TEXT NOT NULL,
    runner             TEXT NOT NULL,
    params             TEXT NOT NULL DEFAULT '{}',
    status             TEXT NOT NULL DEFAULT 'pending'
                           CHECK (status IN ('pending', 'running', 'succeeded', 'failed')),
    exit_code          INTEGER,
    error              TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL,
    started_at         TEXT,
    finished_at        TEXT
);
CREATE INDEX idx_tasks_application ON tasks(application_id);
CREATE INDEX idx_tasks_asset ON tasks(asset_id);
CREATE INDEX idx_tasks_capability ON tasks(capability_id);

CREATE TABLE artifacts (
    id             TEXT PRIMARY KEY,
    task_id        TEXT REFERENCES tasks(id) ON DELETE CASCADE,
    sha256         TEXT NOT NULL,
    media_type     TEXT NOT NULL DEFAULT 'application/octet-stream',
    size           INTEGER NOT NULL,
    kind           TEXT NOT NULL CHECK (kind IN ('input', 'output')),
    name           TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL
);
CREATE INDEX idx_artifacts_task ON artifacts(task_id);
CREATE INDEX idx_artifacts_sha256 ON artifacts(sha256);
