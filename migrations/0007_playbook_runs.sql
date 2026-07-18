-- 0007_playbook_runs: executions of playbooks (P5).
--
-- A playbook run groups the tasks produced by running a playbook's steps against an asset.

CREATE TABLE playbook_runs (
    id          TEXT PRIMARY KEY,
    playbook_id TEXT NOT NULL,
    asset_id    TEXT REFERENCES assets(id) ON DELETE SET NULL,
    actor       TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'succeeded', 'failed')),
    created_at  TEXT NOT NULL,
    finished_at TEXT
);
CREATE INDEX idx_playbook_runs_asset ON playbook_runs(asset_id);

CREATE TABLE playbook_run_tasks (
    run_id  TEXT NOT NULL REFERENCES playbook_runs(id) ON DELETE CASCADE,
    task_id TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    seq     INTEGER NOT NULL,
    PRIMARY KEY (run_id, task_id)
);
CREATE INDEX idx_playbook_run_tasks_run ON playbook_run_tasks(run_id);
