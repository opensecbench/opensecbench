-- 0053_asset_internal_sensitivity: add a middle "internal" tier to asset sensitivity.
--
-- Sensitivity gates whether an asset's content may reach an external LLM (ADR-0011/0020). Until now it
-- was binary — open_source (shareable) or private (never leaves under corporate/strict). That left no
-- room for "ours, not public, but acceptable to send to the model under the default posture." The new
-- ordering is open_source < internal < private, and the corporate profile now permits internal (but not
-- private) egress.
--
-- SQLite can't alter a CHECK constraint in place, so the table is recreated. `assets` is referenced by
-- tasks.asset_id and playbook_runs.asset_id (both ON DELETE SET NULL); dropping the old table would fire
-- those SET-NULLs (foreign_keys is ON and can't be toggled inside a migration transaction), so those
-- links are stashed and restored around the rebuild.

CREATE TEMP TABLE _asset_link_task AS SELECT id, asset_id FROM tasks WHERE asset_id IS NOT NULL;
CREATE TEMP TABLE _asset_link_prun AS SELECT id, asset_id FROM playbook_runs WHERE asset_id IS NOT NULL;

CREATE TABLE assets_new (
    id             TEXT PRIMARY KEY,
    application_id TEXT NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    type           TEXT NOT NULL CHECK (type IN (
                       'source_repo', 'cloud_deployment', 'infrastructure', 'document', 'correspondence')),
    location       TEXT NOT NULL,
    sensitivity    TEXT NOT NULL DEFAULT 'private' CHECK (sensitivity IN ('open_source', 'internal', 'private')),
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);

INSERT INTO assets_new (id, application_id, type, location, sensitivity, created_at, updated_at)
    SELECT id, application_id, type, location, sensitivity, created_at, updated_at FROM assets;

DROP TABLE assets;
ALTER TABLE assets_new RENAME TO assets;

-- Restore the asset links the DROP nulled out (ids are preserved, so they re-point cleanly).
UPDATE tasks
   SET asset_id = (SELECT asset_id FROM _asset_link_task WHERE _asset_link_task.id = tasks.id)
 WHERE id IN (SELECT id FROM _asset_link_task);
UPDATE playbook_runs
   SET asset_id = (SELECT asset_id FROM _asset_link_prun WHERE _asset_link_prun.id = playbook_runs.id)
 WHERE id IN (SELECT id FROM _asset_link_prun);

DROP TABLE _asset_link_task;
DROP TABLE _asset_link_prun;
