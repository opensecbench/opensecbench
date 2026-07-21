-- ADR-0027 + IA declutter: a connector is a GLOBAL external-tracker connection (tracker instance + how to
-- auth), built once in the Library and bound to projects. credential is a vault secret NAME, never a value.
CREATE TABLE connectors (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,               -- display name, e.g. "DefectDojo (prod)"
    type       TEXT NOT NULL,               -- connector type: jira | defectdojo
    base_url   TEXT NOT NULL DEFAULT '',
    credential TEXT NOT NULL DEFAULT '',     -- vault secret name
    created_at TEXT NOT NULL
);;
