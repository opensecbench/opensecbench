-- 0023_proxy_rules: per-project match/replace rules for the proxy's traffic-processor pipeline
-- (ADR-0016 Step 4). Durable configuration (unlike the in-memory intercept queue): rules survive
-- restarts and apply whenever the project's proxy is running.

CREATE TABLE proxy_rules (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    enabled    INTEGER NOT NULL DEFAULT 1,
    target     TEXT NOT NULL CHECK (target IN
                   ('url', 'request_header', 'request_body', 'response_header', 'response_body')),
    match      TEXT NOT NULL,
    replace    TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX idx_proxy_rules_project ON proxy_rules(project_id, created_at);
