-- 0016_secrets: per-project encrypted vault (ADR-0011 + ADR-0049). Same shape as the global secrets
-- table, but sealed with the project's own key (vault.key beside this project.db) so a project stays
-- self-contained. A project secret shadows a global one of the same name at resolution time.
CREATE TABLE secrets (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    sealed     TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);;
