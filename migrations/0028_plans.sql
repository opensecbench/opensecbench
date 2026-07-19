-- Agent playbooks run as plans (ADR-0019 §4): a plan is a DAG of steps, each delegated to a specialist
-- profile, executed in dependency order. Persisted so a run is inspectable and resumable.
CREATE TABLE plans (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL DEFAULT '',
    playbook_id TEXT NOT NULL,
    goal        TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'running', -- running | done | failed
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE TABLE plan_steps (
    id          TEXT PRIMARY KEY,
    plan_id     TEXT NOT NULL REFERENCES plans(id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,
    step_key    TEXT NOT NULL,
    profile     TEXT NOT NULL,
    instruction TEXT NOT NULL,
    depends_on  TEXT NOT NULL DEFAULT '', -- comma-separated step keys
    status      TEXT NOT NULL DEFAULT 'pending', -- pending | running | done | failed | skipped
    result      TEXT NOT NULL DEFAULT '',
    error       TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_plan_steps_plan ON plan_steps(plan_id, seq);
CREATE INDEX idx_plans_project ON plans(project_id, created_at);
