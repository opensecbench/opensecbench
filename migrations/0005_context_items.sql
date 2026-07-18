-- 0005_context_items: ingested unstructured context (docs, emails, chats, notes).
--
-- The bytes live in the CAS as an input artifact (immutable); this table adds project scoping,
-- a type, and a name. Content is retrieved via the artifact it references.

CREATE TABLE context_items (
    id             TEXT PRIMARY KEY,
    project_id     TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    application_id TEXT REFERENCES applications(id) ON DELETE SET NULL,
    type           TEXT NOT NULL CHECK (type IN ('document', 'email', 'chat', 'note')),
    name           TEXT NOT NULL,
    artifact_id    TEXT NOT NULL REFERENCES artifacts(id) ON DELETE CASCADE,
    created_at     TEXT NOT NULL
);
CREATE INDEX idx_context_items_project ON context_items(project_id);
