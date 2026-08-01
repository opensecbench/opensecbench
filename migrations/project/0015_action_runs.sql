-- Action runs (ADR-0059): one execution of a custom action against one finding or observation. The
-- action definition is global (global.db `actions`); a run is per-project because it acts on a
-- project-local subject and its output artifact lives in the project's CAS. This is the durable
-- action-run history a subject shows — "what was run, when, and what came back."
CREATE TABLE action_runs (
    id           TEXT PRIMARY KEY,
    action_id    TEXT NOT NULL,
    action_name  TEXT NOT NULL DEFAULT '',
    kind         TEXT NOT NULL DEFAULT '',    -- "agent" | "script"
    subject_kind TEXT NOT NULL,               -- "finding" | "observation"
    subject_id   TEXT NOT NULL,
    status       TEXT NOT NULL,               -- "running" | "done" | "error"
    summary      TEXT NOT NULL DEFAULT '',
    output       TEXT NOT NULL DEFAULT '',
    artifact_id  TEXT NOT NULL DEFAULT '',
    error        TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    finished_at  TEXT NOT NULL DEFAULT ''
);;

CREATE INDEX idx_action_runs_subject ON action_runs (subject_kind, subject_id, created_at);;
