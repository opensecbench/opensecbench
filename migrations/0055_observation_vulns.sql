-- 0055_observation_vulns: cross-tool observation de-dup (legacy set mirror of project/0008). See that file.
CREATE TABLE observation_vulns (
    id             TEXT PRIMARY KEY,
    observation_id TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    project_id     TEXT NOT NULL,
    vuln_id        TEXT NOT NULL,
    created_at     TEXT NOT NULL,
    UNIQUE (project_id, vuln_id)
);
CREATE INDEX idx_observation_vulns_obs ON observation_vulns(observation_id);
