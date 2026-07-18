-- 0002_core_hierarchy: the assessment hierarchy from ADR-0002.
--
-- organization -> group -> project (engagement) -> application -> asset, plus the durable
-- `target` (a real-world system that survives across engagements) linked to projects many-to-many.
-- IDs are text UUIDs so records stay stable across export/import. Timestamps are RFC3339 text.

CREATE TABLE organizations (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

CREATE TABLE groups (
    id              TEXT PRIMARY KEY,
    organization_id TEXT NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
CREATE INDEX idx_groups_organization ON groups(organization_id);

-- Durable target: holds the knowledge base and prior coverage across engagements.
CREATE TABLE targets (
    id              TEXT PRIMARY KEY,
    organization_id TEXT REFERENCES organizations(id) ON DELETE SET NULL,
    name            TEXT NOT NULL,
    description     TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
CREATE INDEX idx_targets_organization ON targets(organization_id);

-- Project: a time-boxed engagement. Org/group are optional ("if used").
CREATE TABLE projects (
    id              TEXT PRIMARY KEY,
    organization_id TEXT REFERENCES organizations(id) ON DELETE SET NULL,
    group_id        TEXT REFERENCES groups(id) ON DELETE SET NULL,
    name            TEXT NOT NULL,
    status          TEXT NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'archived')),
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
CREATE INDEX idx_projects_organization ON projects(organization_id);
CREATE INDEX idx_projects_group ON projects(group_id);

-- A project references one or more durable targets.
CREATE TABLE project_targets (
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    target_id  TEXT NOT NULL REFERENCES targets(id) ON DELETE CASCADE,
    PRIMARY KEY (project_id, target_id)
);
CREATE INDEX idx_project_targets_target ON project_targets(target_id);

CREATE TABLE applications (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX idx_applications_project ON applications(project_id);

CREATE TABLE assets (
    id             TEXT PRIMARY KEY,
    application_id TEXT NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    type           TEXT NOT NULL CHECK (type IN (
                       'source_repo', 'cloud_deployment', 'infrastructure', 'document', 'correspondence')),
    location       TEXT NOT NULL,
    sensitivity    TEXT NOT NULL DEFAULT 'private' CHECK (sensitivity IN ('open_source', 'private')),
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);
CREATE INDEX idx_assets_application ON assets(application_id);
