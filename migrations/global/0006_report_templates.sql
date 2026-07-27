-- User-authored report templates. Report templates shipped code-defined and read-only (ADR-0003/0008);
-- this lets teams fork the built-ins and author their own, the same "grow your own library" move
-- saved_methodologies (ADR-0055) and saved_playbooks (ADR-0019) made. `md` and `html` are the raw
-- Go-template sources (same shape an extension's Reports[] entry provides); they are parsed into the
-- in-memory report Registry at startup so generation treats user templates exactly like built-ins.
-- `base` records the built-in a template was forked from (provenance only; '' when authored fresh).
CREATE TABLE report_templates (
    id         TEXT PRIMARY KEY,
    title      TEXT NOT NULL,
    kind       TEXT NOT NULL,
    base       TEXT NOT NULL DEFAULT '',
    md         TEXT NOT NULL,
    html       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
