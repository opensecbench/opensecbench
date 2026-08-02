-- Legacy combined-schema mirror of global/0009: the user-configurable data-classification scale.
-- One ordered scale drives both asset sensitivity and destination clearance; see global/0009 for detail.
CREATE TABLE classification_levels (
    id         TEXT PRIMARY KEY,
    label      TEXT NOT NULL,
    rank       INTEGER NOT NULL,
    builtin    INTEGER NOT NULL DEFAULT 0,
    color      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT ''
);

INSERT INTO classification_levels (id, label, rank, builtin, color) VALUES
    ('open_source', 'Open-source only',    0,  1, '#46c07a'),
    ('internal',    'Internal',            10, 1, '#f0a83c'),
    ('private',     'Private (corporate)', 20, 1, '#e0555f');
