-- 0009_classification_levels: the user-configurable data-classification scale (governance).
--
-- One ordered scale drives BOTH asset sensitivity and destination data clearance: content tagged at a
-- level may reach a destination cleared for that level or any more-sensitive one. `rank` orders the scale
-- (higher = more sensitive). The three built-ins reproduce the previous fixed scale and are permanent
-- (renamable/reorderable, never deleted); users may add/reorder/rename/delete custom levels. Ranks use
-- gaps of 10 so custom levels can slot between without renumbering.
CREATE TABLE classification_levels (
    id         TEXT PRIMARY KEY,            -- stable slug; built-ins: open_source/internal/private
    label      TEXT NOT NULL,
    rank       INTEGER NOT NULL,            -- ordering; higher = more sensitive
    builtin    INTEGER NOT NULL DEFAULT 0,
    color      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT ''
);

INSERT INTO classification_levels (id, label, rank, builtin, color) VALUES
    ('open_source', 'Open-source only',    0,  1, '#46c07a'),
    ('internal',    'Internal',            10, 1, '#f0a83c'),
    ('private',     'Private (corporate)', 20, 1, '#e0555f');
