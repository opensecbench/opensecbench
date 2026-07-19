-- User-saved agent playbooks (ADR-0019 step 4). A run can be recorded as a reusable playbook, or one can
-- be authored directly — so teams grow their own library instead of only the built-ins. Steps are a JSON
-- array of {key, profile, instruction, depends_on}.
CREATE TABLE saved_playbooks (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    goal        TEXT NOT NULL DEFAULT '',
    steps       TEXT NOT NULL,
    source      TEXT NOT NULL DEFAULT '', -- e.g. "plan:<id>" when recorded from a run
    created_at  TEXT NOT NULL
);
