-- 0009_http_exchanges: HTTP request/response pairs for the Repeater (and later the proxy) (P7).

CREATE TABLE http_exchanges (
    id               TEXT PRIMARY KEY,
    project_id       TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    name             TEXT NOT NULL DEFAULT '',
    origin           TEXT NOT NULL DEFAULT 'repeater' CHECK (origin IN ('repeater', 'proxy')),
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
CREATE INDEX idx_http_exchanges_project ON http_exchanges(project_id, created_at);
