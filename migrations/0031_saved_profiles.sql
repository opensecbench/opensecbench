-- User-defined agent profiles (ADR-0019 step 4): a custom specialist = a persona + a least-privilege tool
-- allow-list, persisted so teams can craft their own agents alongside the built-ins. Tools is a JSON
-- array of tool names.
CREATE TABLE saved_profiles (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    persona     TEXT NOT NULL,
    tools       TEXT NOT NULL,
    created_at  TEXT NOT NULL
);
