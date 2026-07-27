-- User-authored methodology packs (ADR-0055). The methodology catalog shipped code-defined and read-only
-- (ADR-0009); this lets teams author their own packs and edit copies of the built-ins — the same "grow your
-- own library" move saved_playbooks (ADR-0019) made for agent playbooks. `data` is the full Methodology JSON
-- (id, title, tech, version, keywords, items); it is loaded into the in-memory registry at startup so
-- adoption, coverage, and item lookup treat user packs exactly like built-ins.
CREATE TABLE saved_methodologies (
    id         TEXT PRIMARY KEY,
    title      TEXT NOT NULL,
    data       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
