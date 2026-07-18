-- 0021_coverage_observations: link evidence (observations) to a methodology coverage item, so
-- evidence saved while testing an item attaches to that checklist item (ADR-0009, ADR-0015 Phase 3b).
-- Mirrors finding_observations (0004): a many-to-many curation link, not a schema change to either side.

CREATE TABLE coverage_observations (
    project_id     TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    item_id        TEXT NOT NULL,
    observation_id TEXT NOT NULL REFERENCES observations(id) ON DELETE CASCADE,
    created_at     TEXT NOT NULL,
    PRIMARY KEY (project_id, item_id, observation_id)
);
CREATE INDEX idx_coverage_observations_obs ON coverage_observations(observation_id);
CREATE INDEX idx_coverage_observations_item ON coverage_observations(project_id, item_id);
