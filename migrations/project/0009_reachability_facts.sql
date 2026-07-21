-- 0009_reachability_facts: reachability as an AGGREGATION of facts from many sources (ADR-0031/0034++),
-- not a single boolean. Each source — govulncheck (call graph), opengrep (taint dataflow), route→sink,
-- observed traffic, a human, or an LLM investigation — contributes a fact with its own verdict, confidence,
-- and rationale. The effective reachability of a subject (a CVE or an observation) is resolved from all its
-- facts. This is what lets a manual/LLM determination stand alongside the tool verdicts, especially for
-- dynamic code where static analysis can't find the path.
CREATE TABLE reachability_facts (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL,
    subject_type TEXT NOT NULL,                 -- 'cve' | 'observation'
    subject_key  TEXT NOT NULL,                 -- the CVE/GHSA id, or an observation id
    reachable    TEXT NOT NULL DEFAULT 'unknown' CHECK (reachable IN ('reachable', 'unreachable', 'unknown')),
    confidence   TEXT NOT NULL DEFAULT 'medium' CHECK (confidence IN ('proven', 'high', 'medium', 'low')),
    source       TEXT NOT NULL,                 -- govulncheck | opengrep | route-analysis | traffic | manual | llm
    method       TEXT NOT NULL DEFAULT '',      -- human description of how it was determined
    rationale    TEXT NOT NULL DEFAULT '',      -- evidence / reasoning (a call path, a note, an LLM trace)
    actor        TEXT NOT NULL DEFAULT '',      -- who, for manual/llm
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    UNIQUE (project_id, subject_type, subject_key, source)  -- one current fact per source; sources coexist
);
CREATE INDEX idx_reach_facts_subject ON reachability_facts(project_id, subject_type, subject_key);
