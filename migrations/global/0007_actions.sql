-- Custom actions (ADR-0059): user-authored operations run against a single finding or observation —
-- an LLM agent (delegate to a saved profile) or a sandboxed script (a templated RunSpec). Stored
-- globally like saved_profiles / saved_playbooks so an action is reusable across projects; the built-in
-- examples are code-defined and merged in at read time, so only user-authored actions land here.
-- Structured fields (subject_kinds, applies_when, cmd, output) are JSON — the same "grow your own
-- library" shape the other saved_* tables use.
CREATE TABLE actions (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    icon            TEXT NOT NULL DEFAULT '',
    kind            TEXT NOT NULL,               -- "agent" | "script"
    subject_kinds   TEXT NOT NULL DEFAULT '[]',  -- JSON array: "finding","observation"
    applies_when    TEXT NOT NULL DEFAULT '{}',  -- JSON Predicate
    technique       TEXT NOT NULL DEFAULT '',    -- ROE technique; '' = passive
    profile_id      TEXT NOT NULL DEFAULT '',    -- agent kind
    instruction     TEXT NOT NULL DEFAULT '',    -- agent kind (templated)
    image           TEXT NOT NULL DEFAULT '',    -- script kind
    cmd             TEXT NOT NULL DEFAULT '[]',  -- script kind (JSON array, templated)
    network         TEXT NOT NULL DEFAULT '',
    timeout_seconds INTEGER NOT NULL DEFAULT 0,
    memory_mb       INTEGER NOT NULL DEFAULT 0,
    cpus            REAL NOT NULL DEFAULT 0,
    output          TEXT NOT NULL DEFAULT '{}',  -- JSON OutputSpec
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);;
