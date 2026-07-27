-- User-authored methodology packs (ADR-0055). Mirrors global/0005_saved_methodologies.sql for the legacy
-- single-database layout (tests + transitional combined mode); the two-tier production path applies the
-- global/ copy. `data` is the full methodology.Methodology JSON, loaded into the registry at startup so user
-- packs behave like the code-defined built-ins (ADR-0009).
CREATE TABLE saved_methodologies (
    id         TEXT PRIMARY KEY,
    title      TEXT NOT NULL,
    data       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
