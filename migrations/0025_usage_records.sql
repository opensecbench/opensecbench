-- 0025_usage_records: per-run Analyst token accounting, tagged by project + provider/vendor + model,
-- so token use can be compared across models and vendors (the plan's `usage_record`). Cost can be
-- derived later from provider rate cards; we store the raw counts.

CREATE TABLE usage_records (
    id            TEXT PRIMARY KEY,
    project_id    TEXT REFERENCES projects(id) ON DELETE CASCADE, -- nullable: not every run is project-scoped
    thread_id     TEXT NOT NULL DEFAULT '',
    provider      TEXT NOT NULL,                                  -- vendor/type: anthropic | openai | ollama | claude-cli | …
    model         TEXT NOT NULL DEFAULT '',
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL
);
CREATE INDEX idx_usage_project ON usage_records(project_id, created_at);
