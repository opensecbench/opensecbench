-- 0015_kb: durable knowledge base entries anchored to a target (ADR-0010). Projects inherit KB via
-- the existing project_targets link, so re-assessing a known target starts ahead.

CREATE TABLE kb_entries (
    id           TEXT PRIMARY KEY,
    target_id    TEXT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL,
    scope        TEXT NOT NULL DEFAULT 'target' CHECK (scope IN ('target', 'group', 'org', 'global')),
    title        TEXT NOT NULL,
    body         TEXT NOT NULL DEFAULT '',
    tags         TEXT NOT NULL DEFAULT '',
    sensitivity  TEXT NOT NULL DEFAULT 'private' CHECK (sensitivity IN ('open_source', 'private')),
    origin       TEXT NOT NULL DEFAULT 'human' CHECK (origin IN ('human', 'thread', 'derived')),
    review_state TEXT NOT NULL DEFAULT 'unreviewed' CHECK (review_state IN ('unreviewed', 'confirmed', 'rejected')),
    source_ref   TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL
);
CREATE INDEX idx_kb_target ON kb_entries(target_id);
