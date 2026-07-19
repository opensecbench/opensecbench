-- 0041_investigation_vulns: cross-tool vulnerability de-duplication (ADR-0037). grype and govulncheck (and
-- other SCA tools) each report the same CVE under different advisory schemes (grype → GHSA, govulncheck →
-- CVE+GHSA), so the same underlying vulnerability spawned a separate investigation per tool. This maps each
-- advisory id a project is investigating to its investigation, UNIQUE per (project, vuln id) — so a second
-- tool's finding for a vuln already under investigation does not open a duplicate.

CREATE TABLE investigation_vulns (
    id               TEXT PRIMARY KEY,
    investigation_id TEXT NOT NULL REFERENCES investigations(id) ON DELETE CASCADE,
    project_id       TEXT NOT NULL,
    vuln_id          TEXT NOT NULL, -- a CVE or GHSA advisory id
    created_at       TEXT NOT NULL,
    UNIQUE (project_id, vuln_id)
);
CREATE INDEX idx_investigation_vulns_inv ON investigation_vulns(investigation_id);
