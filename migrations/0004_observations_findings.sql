-- 0004_observations_findings: reviewable evidence + conclusions (ADR-0002, ADR-0005).
--
-- Observations are interpreted results (from a tool, a thread, or a human), each with an origin
-- and a review state. Findings are reviewed security conclusions, supported_by observations.
-- Provenance: finding -> observation -> (task, artifact) -> capability+version, runner.

CREATE TABLE observations (
    id           TEXT PRIMARY KEY,
    task_id      TEXT REFERENCES tasks(id) ON DELETE CASCADE,
    artifact_id  TEXT REFERENCES artifacts(id) ON DELETE SET NULL,
    origin       TEXT NOT NULL CHECK (origin IN ('tool', 'thread', 'human')),
    review_state TEXT NOT NULL DEFAULT 'unreviewed'
                     CHECK (review_state IN ('unreviewed', 'confirmed', 'rejected')),
    title        TEXT NOT NULL,
    detail       TEXT NOT NULL DEFAULT '',
    severity     TEXT NOT NULL DEFAULT 'info'
                     CHECK (severity IN ('info', 'low', 'medium', 'high', 'critical')),
    rule_id      TEXT NOT NULL DEFAULT '',
    location     TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL
);
CREATE INDEX idx_observations_task ON observations(task_id);
CREATE INDEX idx_observations_artifact ON observations(artifact_id);
CREATE INDEX idx_observations_review ON observations(review_state);

CREATE TABLE findings (
    id             TEXT PRIMARY KEY,
    application_id TEXT REFERENCES applications(id) ON DELETE SET NULL,
    title          TEXT NOT NULL,
    severity       TEXT NOT NULL DEFAULT 'medium'
                       CHECK (severity IN ('info', 'low', 'medium', 'high', 'critical')),
    status         TEXT NOT NULL DEFAULT 'open'
                       CHECK (status IN ('open', 'confirmed', 'remediated', 'accepted', 'false_positive')),
    description    TEXT NOT NULL DEFAULT '',
    cwe            TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
);
CREATE INDEX idx_findings_application ON findings(application_id);

-- supported_by: a finding is backed by one or more confirmed observations.
CREATE TABLE finding_observations (
    finding_id     TEXT NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    observation_id TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    PRIMARY KEY (finding_id, observation_id)
);
CREATE INDEX idx_finding_observations_observation ON finding_observations(observation_id);
