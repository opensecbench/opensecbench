-- 0001_init: schema migration bookkeeping.
--
-- The full OpenSecBench schema (organizations, targets, projects, capabilities, artifacts,
-- observations, evidence, findings, audit, ...) is introduced in phase P1 alongside
-- docs/adr-0002-data-model-and-provenance.md. This initial migration establishes only the
-- tracking table used to record which migrations have been applied.
--
-- TODO(P1): add the core domain tables per ADR-0002.
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    applied_at TEXT NOT NULL
);
