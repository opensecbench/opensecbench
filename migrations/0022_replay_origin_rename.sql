-- 0022_replay_origin_rename: the "Repeater" HTTP tool was renamed to "Replay" (ADR-0016). Rename the
-- exchange origin value + CHECK constraint to match. SQLite cannot ALTER a CHECK, so rebuild the table
-- (standard rename-copy-drop) and migrate existing 'repeater' rows to 'replay'.

ALTER TABLE http_exchanges RENAME TO http_exchanges_old;

CREATE TABLE http_exchanges (
    id               TEXT PRIMARY KEY,
    project_id       TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name             TEXT NOT NULL DEFAULT '',
    origin           TEXT NOT NULL DEFAULT 'replay' CHECK (origin IN ('replay', 'proxy')),
    method           TEXT NOT NULL,
    url              TEXT NOT NULL,
    request_headers  TEXT NOT NULL DEFAULT '',
    request_body     TEXT NOT NULL DEFAULT '',
    status           INTEGER,
    response_headers TEXT NOT NULL DEFAULT '',
    response_body    TEXT NOT NULL DEFAULT '',
    duration_ms      INTEGER,
    created_at       TEXT NOT NULL,
    sent_at          TEXT
);

INSERT INTO http_exchanges (id, project_id, name, origin, method, url, request_headers,
    request_body, status, response_headers, response_body, duration_ms, created_at, sent_at)
SELECT id, project_id, name,
    CASE origin WHEN 'repeater' THEN 'replay' ELSE origin END,
    method, url, request_headers, request_body, status, response_headers, response_body,
    duration_ms, created_at, sent_at
FROM http_exchanges_old;

DROP TABLE http_exchanges_old;
CREATE INDEX idx_http_exchanges_project ON http_exchanges(project_id, created_at);
