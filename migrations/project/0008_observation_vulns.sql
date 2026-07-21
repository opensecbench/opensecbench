-- 0008_observation_vulns: cross-tool observation de-duplication (ADR-0037/0031). The same vulnerability is
-- reported by multiple SCA tools under different advisory ids (grype→GHSA, osv-scanner→CVE, govulncheck→GO
-- id + CVE alias). This claims each advisory id for the first observation that reports it, so a later tool's
-- report of the same vuln merges into that observation instead of creating a duplicate. Mirrors
-- investigation_vulns (which already de-dups at the investigation level).
CREATE TABLE observation_vulns (
    id             TEXT PRIMARY KEY,
    observation_id TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    project_id     TEXT NOT NULL,
    vuln_id        TEXT NOT NULL, -- a CVE or GHSA advisory id
    created_at     TEXT NOT NULL,
    UNIQUE (project_id, vuln_id)
);
CREATE INDEX idx_observation_vulns_obs ON observation_vulns(observation_id);
