-- 0019_settings: a small key/value store for control-plane settings (e.g. the active policy
-- profile). Kept generic so simple singleton settings need no bespoke tables.

CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
