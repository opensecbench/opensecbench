-- 0018_traffic_rules: unified per-project match→action rules for the proxy (ADR-0016). One CEL match
-- (pkg/httpfilter) plus one action, evaluated top→bottom per phase. Supersedes proxy_rules (match/replace,
-- never ported to the split schema) and intercept arming: interception is now the 'hold' action.
-- Mirrors migrations/0067_traffic_rules.sql (root/combined schema used by tests).

CREATE TABLE traffic_rules (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    seq        INTEGER NOT NULL DEFAULT 0,
    enabled    INTEGER NOT NULL DEFAULT 1,
    phase      TEXT NOT NULL CHECK (phase IN ('request', 'response', 'both')),
    match_expr TEXT NOT NULL DEFAULT '',
    action     TEXT NOT NULL CHECK (action IN
                   ('hold', 'drop', 'set_header', 'remove_header', 'replace_body', 'set_status')),
    params     TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
);
CREATE INDEX idx_traffic_rules_project ON traffic_rules(project_id, seq);
