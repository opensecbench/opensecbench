-- 0039_reachability: a project-level reachability verdict per CVE (ADR-0031). A reachability analyzer
-- (govulncheck, ADR-0030) proves whether a vulnerability's code is actually called; that verdict is a fact
-- about the CVE, not the tool, so it is stored here and reused to make any SCA tool's CVE findings (e.g.
-- grype, across ecosystems) reachability-aware.

CREATE TABLE reachability (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
    cve        TEXT NOT NULL,
    package    TEXT NOT NULL DEFAULT '',
    reachable  INTEGER NOT NULL,         -- 0 = imported/uncalled, 1 = called (in the call graph)
    source     TEXT NOT NULL DEFAULT '', -- the analyzer that produced the verdict, e.g. govulncheck
    updated_at TEXT NOT NULL,
    UNIQUE (project_id, cve)
);
CREATE INDEX idx_reachability_project ON reachability(project_id);
