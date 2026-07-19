-- 0033_tasks_durable_queue: make the task queue durable (ADR-0023). The tasks row becomes the queue
-- itself, so pending work survives a control-plane restart and interrupted runs resume. Reconstructing a
-- queued task needs two things not previously persisted — the secret-reference map (envVar -> vault
-- secret NAME; never resolved values, per ADR-0011) and a raw target_dir supplied without an asset —
-- plus an attempts counter to bound re-runs of a task that keeps crashing the process.

ALTER TABLE tasks ADD COLUMN secret_refs TEXT NOT NULL DEFAULT '';    -- JSON {envVar: vaultName}; '' = none
ALTER TABLE tasks ADD COLUMN target_dir  TEXT NOT NULL DEFAULT '';    -- raw dir when no asset; '' otherwise
ALTER TABLE tasks ADD COLUMN attempts    INTEGER NOT NULL DEFAULT 0;  -- claim count, for the interruption retry cap

-- The worker pool claims the oldest pending task; index the claim query.
CREATE INDEX idx_tasks_pending ON tasks(status, created_at);
