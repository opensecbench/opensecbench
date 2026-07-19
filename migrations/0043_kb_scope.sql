-- 0043_kb_scope: knowledge above the target (ADR-0041). Until now every KB entry was anchored to a single
-- target, so org/team-level knowledge (a shared auth provider, org-wide conventions, common infra) had
-- nowhere durable to live and could not be reused across a team's apps. This makes the existing `scope`
-- column real: an entry anchors to a target, a group, an organization, or is global — and a project inherits
-- all four levels. SQLite can't drop a NOT NULL in place, so the table is recreated (existing rows are all
-- target-scoped).

CREATE TABLE kb_entries_new (
    id              TEXT PRIMARY KEY,
    scope           TEXT NOT NULL DEFAULT 'target' CHECK (scope IN ('target', 'group', 'org', 'global')),
    target_id       TEXT REFERENCES targets(id) ON DELETE CASCADE,
    group_id        TEXT REFERENCES groups(id) ON DELETE CASCADE,
    organization_id TEXT REFERENCES organizations(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL,
    title           TEXT NOT NULL,
    body            TEXT NOT NULL DEFAULT '',
    tags            TEXT NOT NULL DEFAULT '',
    sensitivity     TEXT NOT NULL DEFAULT 'private' CHECK (sensitivity IN ('open_source', 'private')),
    origin          TEXT NOT NULL DEFAULT 'human' CHECK (origin IN ('human', 'thread', 'derived')),
    review_state    TEXT NOT NULL DEFAULT 'unreviewed' CHECK (review_state IN ('unreviewed', 'confirmed', 'rejected')),
    source_ref      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    -- exactly the anchor for the scope is set (global anchors to nothing).
    CHECK (
        (scope = 'target' AND target_id IS NOT NULL AND group_id IS NULL AND organization_id IS NULL) OR
        (scope = 'group'  AND group_id  IS NOT NULL AND target_id IS NULL AND organization_id IS NULL) OR
        (scope = 'org'    AND organization_id IS NOT NULL AND target_id IS NULL AND group_id IS NULL) OR
        (scope = 'global' AND target_id IS NULL AND group_id IS NULL AND organization_id IS NULL)
    )
);

INSERT INTO kb_entries_new (id, scope, target_id, group_id, organization_id, kind, title, body, tags, sensitivity, origin, review_state, source_ref, created_at, updated_at)
    SELECT id, 'target', target_id, NULL, NULL, kind, title, body, tags, sensitivity, origin, review_state, source_ref, created_at, updated_at
    FROM kb_entries;

DROP TABLE kb_entries;
ALTER TABLE kb_entries_new RENAME TO kb_entries;

CREATE INDEX idx_kb_target ON kb_entries(target_id);
CREATE INDEX idx_kb_group ON kb_entries(group_id);
CREATE INDEX idx_kb_org ON kb_entries(organization_id);
CREATE INDEX idx_kb_scope ON kb_entries(scope);
