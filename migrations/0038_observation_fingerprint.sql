-- 0038_observation_fingerprint: content-addressed dedup for observations (ADR-0029). Every capability run
-- previously minted a fresh observation, so a re-scan duplicated findings and re-fired post-run
-- dispositions — re-opening investigations and re-seeding agent threads (burning tokens) for issues we had
-- already seen. A stable, content-derived fingerprint lets the engine recognise the same finding across
-- runs and skip it (no duplicate observation, no repeated disposition).

ALTER TABLE observations ADD COLUMN fingerprint TEXT NOT NULL DEFAULT ''; -- sha256(origin|rule|location|detail); '' = not fingerprinted

-- Scoped lookup: "have we already seen this fingerprint in this project?" (best-effort skip in the engine).
CREATE INDEX idx_observations_fingerprint ON observations(project_id, fingerprint);
