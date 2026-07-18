-- 0018_external_links: idempotent links between OSB findings and external issue trackers (P10).
-- A finding can be pushed to a given integration at most once; re-push returns the existing link.

CREATE TABLE external_links (
    id           TEXT PRIMARY KEY,
    finding_id   TEXT NOT NULL REFERENCES findings(id) ON DELETE CASCADE,
    integration  TEXT NOT NULL,
    external_id  TEXT NOT NULL,
    external_url TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    UNIQUE (finding_id, integration)
);
CREATE INDEX idx_external_links_finding ON external_links(finding_id);
