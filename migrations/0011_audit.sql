-- 0011_audit: the persisted, hash-chained audit trail (ADR-0001, ADR-0002). Each event links to
-- the previous one by hash, so tampering with earlier rows is detectable.

CREATE TABLE audit_events (
    seq       INTEGER PRIMARY KEY,
    time      TEXT NOT NULL,
    actor     TEXT NOT NULL,
    action    TEXT NOT NULL,
    target    TEXT NOT NULL DEFAULT '',
    data      TEXT NOT NULL DEFAULT '',
    prev_hash TEXT NOT NULL DEFAULT '',
    hash      TEXT NOT NULL
);
