-- 0017_dlp: planted canary tokens (exfil tripwires) and the DLP event trail (ADR-0011).

CREATE TABLE canaries (
    id         TEXT PRIMARY KEY,
    label      TEXT NOT NULL,
    token      TEXT NOT NULL UNIQUE,
    created_at TEXT NOT NULL
);

CREATE TABLE dlp_events (
    id         TEXT PRIMARY KEY,
    kind       TEXT NOT NULL,   -- secret | canary | pattern
    label      TEXT NOT NULL,
    action     TEXT NOT NULL,   -- block | alert
    blocked    INTEGER NOT NULL DEFAULT 0,
    location   TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX idx_dlp_events_time ON dlp_events(created_at);
