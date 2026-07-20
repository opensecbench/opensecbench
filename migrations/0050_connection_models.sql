-- ADR-0052 (legacy combined schema mirror of global/0002): a connection serves many models, discovered
-- live and enriched by the curated overlay; connection_models caches that per-connection set.
CREATE TABLE connection_models (
    connection_id   TEXT    NOT NULL,
    model_id        TEXT    NOT NULL,
    display_name    TEXT    NOT NULL DEFAULT '',
    family          TEXT    NOT NULL DEFAULT '',
    context_window  INTEGER NOT NULL DEFAULT 0,
    input_per_mtok  REAL    NOT NULL DEFAULT 0,
    output_per_mtok REAL    NOT NULL DEFAULT 0,
    tags            TEXT    NOT NULL DEFAULT '',
    source          TEXT    NOT NULL DEFAULT '',
    last_seen       TEXT    NOT NULL,
    PRIMARY KEY (connection_id, model_id)
);;

ALTER TABLE providers ADD COLUMN models_refreshed_at TEXT NOT NULL DEFAULT '';;
