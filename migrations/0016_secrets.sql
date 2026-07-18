-- 0016_secrets: the encrypted vault (ADR-0011). Only sealed (AES-256-GCM) values are stored; they
-- are referenced by name and never returned in plaintext through the API.

CREATE TABLE secrets (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    sealed     TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
