-- ADR-0051: project-level base path — relative asset locations resolve against it.
ALTER TABLE engagement ADD COLUMN base_path TEXT NOT NULL DEFAULT '';
