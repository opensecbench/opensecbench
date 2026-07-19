-- 0034_runners: remote outbound-connect runners (ADR-0024). A runner is an agent that dials home to the
-- control plane, authenticates with an ed25519 key established at enrollment, and executes capability
-- tasks from its own network vantage. Enrollment tokens are one-time bearer secrets — we store only their
-- sha256 (never the token itself), consumed atomically at enroll.

CREATE TABLE runners (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    pubkey      TEXT NOT NULL,                         -- base64 ed25519 public key (authenticates the runner)
    status      TEXT NOT NULL DEFAULT 'active'
                    CHECK (status IN ('active', 'revoked')),
    enrolled_at TEXT NOT NULL,
    last_seen   TEXT
);

CREATE TABLE runner_enroll_tokens (
    token_hash TEXT PRIMARY KEY,                       -- sha256(token) hex; the token is shown once to the operator
    label      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    used_at    TEXT                                    -- set when consumed; NULL = still valid
);

-- Which runner a task targets: '' (default) runs on the local Docker runner; otherwise a runners.id.
-- Persisted so the durable queue can re-dispatch a queued task to the right runner after a restart.
ALTER TABLE tasks ADD COLUMN runner_target TEXT NOT NULL DEFAULT '';
