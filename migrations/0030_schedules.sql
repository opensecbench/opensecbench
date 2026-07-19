-- Scheduled playbook runs (ADR-0019 step 4): for large, ongoing engagements, a playbook runs on a
-- cadence (e.g. daily/weekly) against a baseline. Scheduled ≠ looping — each firing is one bounded plan,
-- then it waits. The scheduler fires enabled schedules whose next_run_at has passed.
CREATE TABLE schedules (
    id               TEXT PRIMARY KEY,
    project_id       TEXT NOT NULL,
    playbook_id      TEXT NOT NULL,
    interval_seconds INTEGER NOT NULL,
    enabled          INTEGER NOT NULL DEFAULT 1,
    last_run_at      TEXT,
    next_run_at      TEXT NOT NULL,
    created_at       TEXT NOT NULL
);

CREATE INDEX idx_schedules_due ON schedules(enabled, next_run_at);
