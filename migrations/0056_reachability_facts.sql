-- 0056_reachability_facts: reachability aggregation store (legacy set mirror of project/0009). See that file.
CREATE TABLE reachability_facts (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL,
    subject_type TEXT NOT NULL,
    subject_key  TEXT NOT NULL,
    reachable    TEXT NOT NULL DEFAULT 'unknown' CHECK (reachable IN ('reachable', 'unreachable', 'unknown')),
    confidence   TEXT NOT NULL DEFAULT 'medium' CHECK (confidence IN ('proven', 'high', 'medium', 'low')),
    source       TEXT NOT NULL,
    method       TEXT NOT NULL DEFAULT '',
    rationale    TEXT NOT NULL DEFAULT '',
    actor        TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    UNIQUE (project_id, subject_type, subject_key, source)
);
CREATE INDEX idx_reach_facts_subject ON reachability_facts(project_id, subject_type, subject_key);
