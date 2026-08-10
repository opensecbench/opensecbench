-- Legacy combined-schema mirror of project/0020: expand the asset model with new types (domain, host, endpoint), provenance
-- axes (origin, verification_state), tags, metadata, and status lifecycle; add the entity_links graph
-- and research_items table; add engagement fields for program/runtime policy (ADR-0071).
--
-- SQLite can't ALTER CHECK constraints, so the assets table is rebuilt. FK links from tasks and
-- playbook_runs are stashed and restored around the rebuild.

-- Stash FK references to assets.
CREATE TEMP TABLE _asset_link_task AS SELECT id, asset_id FROM tasks WHERE asset_id IS NOT NULL;
CREATE TEMP TABLE _asset_link_prun AS SELECT id, asset_id FROM playbook_runs WHERE asset_id IS NOT NULL;

CREATE TABLE assets_new (
    id                 TEXT PRIMARY KEY,
    application_id     TEXT NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    type               TEXT NOT NULL CHECK (type IN (
                           'source_repo', 'web_service', 'cloud_deployment', 'infrastructure',
                           'document', 'correspondence', 'domain', 'host', 'endpoint')),
    location           TEXT NOT NULL,
    sensitivity        TEXT NOT NULL DEFAULT 'private',
    ecosystems         TEXT NOT NULL DEFAULT '',
    status             TEXT NOT NULL DEFAULT 'confirmed' CHECK (status IN (
                           'discovered', 'confirmed', 'investigating', 'tested')),
    tags               TEXT NOT NULL DEFAULT '[]',
    metadata           TEXT NOT NULL DEFAULT '{}',
    origin             TEXT NOT NULL DEFAULT 'manual' CHECK (origin IN (
                           'manual', 'tool', 'agent', 'proxy', 'import')),
    verification_state TEXT NOT NULL DEFAULT 'verified' CHECK (verification_state IN (
                           'unverified', 'observed', 'corroborated', 'verified', 'disputed')),
    first_seen         TEXT NOT NULL DEFAULT '',
    last_seen          TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);

INSERT INTO assets_new (id, application_id, type, location, sensitivity, ecosystems, created_at, updated_at,
                        first_seen, last_seen)
    SELECT id, application_id, type, location, sensitivity, ecosystems, created_at, updated_at,
           created_at, updated_at
    FROM assets;

DROP TABLE assets;
ALTER TABLE assets_new RENAME TO assets;

-- Restore FK references.
UPDATE tasks
   SET asset_id = (SELECT asset_id FROM _asset_link_task WHERE _asset_link_task.id = tasks.id)
 WHERE id IN (SELECT id FROM _asset_link_task);
UPDATE playbook_runs
   SET asset_id = (SELECT asset_id FROM _asset_link_prun WHERE _asset_link_prun.id = playbook_runs.id)
 WHERE id IN (SELECT id FROM _asset_link_prun);

DROP TABLE _asset_link_task;
DROP TABLE _asset_link_prun;

CREATE UNIQUE INDEX idx_assets_app_type_location ON assets(application_id, type, location);

-- Entity links: generic graph primitive for topology, research chains, evidence (ADR-0071).
CREATE TABLE entity_links (
    id           TEXT PRIMARY KEY,
    source_type  TEXT NOT NULL,
    source_id    TEXT NOT NULL,
    relationship TEXT NOT NULL,
    target_type  TEXT NOT NULL,
    target_id    TEXT NOT NULL,
    metadata     TEXT NOT NULL DEFAULT '{}',
    note         TEXT,
    created_at   TEXT NOT NULL
);
CREATE INDEX idx_entity_links_source ON entity_links(source_type, source_id);
CREATE INDEX idx_entity_links_target ON entity_links(target_type, target_id);

-- Research items: investigator notes, hypotheses, experiments (ADR-0071).
CREATE TABLE research_items (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL,
    type       TEXT NOT NULL CHECK (type IN (
                   'note', 'hypothesis', 'lead', 'question', 'experiment', 'result', 'conclusion')),
    title      TEXT NOT NULL,
    body       TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL DEFAULT 'open' CHECK (status IN ('open', 'active', 'resolved', 'discarded')),
    assessment TEXT NOT NULL DEFAULT '',
    created_by TEXT NOT NULL DEFAULT 'manual',
    tags       TEXT NOT NULL DEFAULT '[]',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_research_items_project ON research_items(project_id);

-- Scope entries: add 'url' kind (ADR-0071). Rebuild required for CHECK constraint.
CREATE TABLE scope_entries_new (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('host', 'domain', 'cidr', 'url')),
    value       TEXT NOT NULL,
    disposition TEXT NOT NULL DEFAULT 'allow',
    created_at  TEXT NOT NULL
);
INSERT INTO scope_entries_new (id, project_id, kind, value, disposition, created_at)
    SELECT id, project_id, kind, value, disposition, created_at FROM scope_entries;
DROP TABLE scope_entries;
ALTER TABLE scope_entries_new RENAME TO scope_entries;
CREATE INDEX idx_scope_entries_project ON scope_entries(project_id);

-- Engagement: program + runtime policy fields (ADR-0071).
ALTER TABLE engagement ADD COLUMN program_url      TEXT NOT NULL DEFAULT '';
ALTER TABLE engagement ADD COLUMN platform         TEXT NOT NULL DEFAULT '';
ALTER TABLE engagement ADD COLUMN scope_doc_ref    TEXT NOT NULL DEFAULT '';
ALTER TABLE engagement ADD COLUMN runtime_image    TEXT NOT NULL DEFAULT '';
ALTER TABLE engagement ADD COLUMN runtime_network  TEXT NOT NULL DEFAULT '';
