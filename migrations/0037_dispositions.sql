-- 0037_dispositions: post-run disposition routing (ADR-0028). After a capability's output is interpreted
-- into observations, rules route each to an action — auto-promote to a finding, open an investigation, or
-- leave for manual review. Observations gain a structured attributes bag so interpreters can carry facts
-- (e.g. TruffleHog verified=true) that rules match on.

ALTER TABLE observations ADD COLUMN attributes TEXT NOT NULL DEFAULT ''; -- JSON map {key: value}; '' = none

-- A tracked investigation opened for an observation that needs validation (unverified secret, etc.). A
-- human (or the agent, on demand) works it, ending in human-validated findings.
CREATE TABLE investigations (
    id             TEXT PRIMARY KEY,
    project_id     TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    application_id TEXT REFERENCES applications(id) ON DELETE SET NULL,
    observation_id TEXT NOT NULL UNIQUE REFERENCES observations(id) ON DELETE CASCADE,
    title          TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'open'
                       CHECK (status IN ('open', 'investigating', 'resolved', 'dismissed')),
    thread_id      TEXT REFERENCES threads(id) ON DELETE SET NULL, -- the agent investigation thread, once run
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);
CREATE INDEX idx_investigations_project ON investigations(project_id, status);

-- Per-project disposition overrides; capability_id '' applies to all capabilities. Rules are consulted
-- before a capability's manifest-declared defaults.
CREATE TABLE disposition_rules (
    id            TEXT PRIMARY KEY,
    project_id    TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    capability_id TEXT NOT NULL DEFAULT '',
    when_json     TEXT NOT NULL DEFAULT '{}', -- attribute match {key: value}
    min_severity  TEXT NOT NULL DEFAULT '',
    action        TEXT NOT NULL CHECK (action IN ('finding', 'investigate', 'review')),
    priority      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL
);
CREATE INDEX idx_disposition_rules_project ON disposition_rules(project_id);
