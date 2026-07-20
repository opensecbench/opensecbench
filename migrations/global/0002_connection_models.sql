-- ADR-0052: a connection (a providers row) exposes MANY models, discovered live from the backend and
-- enriched by the curated overlay. This table caches that discovered+enriched set per connection so the
-- picker and routing draw from what the backend actually serves, not a static list. source is
-- "live" (from the backend's model list), "overlay" (curated fallback when discovery is unavailable),
-- or "custom" (a model id the operator pinned by hand).
CREATE TABLE connection_models (
    connection_id   TEXT    NOT NULL,               -- providers.id
    model_id        TEXT    NOT NULL,               -- id served by the connection (e.g. claude-sonnet-5)
    display_name    TEXT    NOT NULL DEFAULT '',
    family          TEXT    NOT NULL DEFAULT '',     -- normalized family for metadata sharing across connections
    context_window  INTEGER NOT NULL DEFAULT 0,
    input_per_mtok  REAL    NOT NULL DEFAULT 0,
    output_per_mtok REAL    NOT NULL DEFAULT 0,
    tags            TEXT    NOT NULL DEFAULT '',     -- JSON array of routing/label tags
    source          TEXT    NOT NULL DEFAULT '',     -- live | overlay | custom
    last_seen       TEXT    NOT NULL,
    PRIMARY KEY (connection_id, model_id)
);;

-- When the connection's model set was last discovered. Empty = never refreshed.
ALTER TABLE providers ADD COLUMN models_refreshed_at TEXT NOT NULL DEFAULT '';;
