-- ADR-0027 + IA declutter: split per-project integration config into a global connector (see global/0004)
-- + a per-project BINDING that attaches this project to a connector with a project-side scope. Replaces
-- the old integration_configs (which conflated connection + binding). No data preserved.
DROP TABLE IF EXISTS integration_configs;;

CREATE TABLE integration_bindings (
    id           TEXT NOT NULL PRIMARY KEY,
    project_id   TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    connector_id TEXT NOT NULL,              -- connectors.id (global.db)
    project_key  TEXT NOT NULL DEFAULT '',   -- tracker-side scope, e.g. a DefectDojo test id
    created_at   TEXT NOT NULL,
    UNIQUE (project_id, connector_id)
);;

-- Re-key the inbound-pull dedupe ledger to the connector id.
DROP TABLE IF EXISTS integration_imports;;
CREATE TABLE integration_imports (
    id             TEXT NOT NULL PRIMARY KEY,
    project_id     TEXT NOT NULL,
    connector_id   TEXT NOT NULL,
    external_id    TEXT NOT NULL,
    observation_id TEXT NOT NULL,
    imported_at    TEXT NOT NULL,
    UNIQUE (project_id, connector_id, external_id)
);;
