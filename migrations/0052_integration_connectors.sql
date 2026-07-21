-- ADR-0027 + IA declutter (legacy combined-schema mirror of global/0004 + project/0005): global connectors
-- + per-project bindings, replacing integration_configs. No data preserved.
CREATE TABLE connectors (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    type       TEXT NOT NULL,
    base_url   TEXT NOT NULL DEFAULT '',
    credential TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);;

DROP TABLE IF EXISTS integration_configs;;

CREATE TABLE integration_bindings (
    id           TEXT NOT NULL PRIMARY KEY,
    project_id   TEXT NOT NULL,
    connector_id TEXT NOT NULL,
    project_key  TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    UNIQUE (project_id, connector_id)
);;

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
