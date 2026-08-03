-- 0019_asset_web_service: add 'web_service' to the assets.type CHECK — a reachable HTTP(S) target at a
-- base URL (ADR-0067), so a live site is a first-class scannable asset rather than a misfiled source_repo.
--
-- SQLite can't alter a CHECK in place, so the table is recreated (same rebuild dance as 0017). `assets` is
-- referenced by tasks.asset_id and playbook_runs.asset_id (ON DELETE SET NULL); those links are stashed and
-- restored around the rebuild so the DROP doesn't null them (foreign_keys can't be toggled mid-migration).

CREATE TEMP TABLE _asset_link_task AS SELECT id, asset_id FROM tasks WHERE asset_id IS NOT NULL;
CREATE TEMP TABLE _asset_link_prun AS SELECT id, asset_id FROM playbook_runs WHERE asset_id IS NOT NULL;

CREATE TABLE assets_new (
    id             TEXT PRIMARY KEY,
    application_id TEXT NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    type           TEXT NOT NULL CHECK (type IN (
                       'source_repo', 'web_service', 'cloud_deployment', 'infrastructure', 'document', 'correspondence')),
    location       TEXT NOT NULL,
    sensitivity    TEXT NOT NULL DEFAULT 'private',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL,
    ecosystems     TEXT NOT NULL DEFAULT ''
);

INSERT INTO assets_new (id, application_id, type, location, sensitivity, created_at, updated_at, ecosystems)
    SELECT id, application_id, type, location, sensitivity, created_at, updated_at, ecosystems FROM assets;

DROP TABLE assets;
ALTER TABLE assets_new RENAME TO assets;

UPDATE tasks
   SET asset_id = (SELECT asset_id FROM _asset_link_task WHERE _asset_link_task.id = tasks.id)
 WHERE id IN (SELECT id FROM _asset_link_task);
UPDATE playbook_runs
   SET asset_id = (SELECT asset_id FROM _asset_link_prun WHERE _asset_link_prun.id = playbook_runs.id)
 WHERE id IN (SELECT id FROM _asset_link_prun);

DROP TABLE _asset_link_task;
DROP TABLE _asset_link_prun;
