-- 0010_project_index_dir: optional custom on-disk location for a project's self-contained directory.
-- Empty = the default <data>/projects/<id>. When set, project.db + cas/ + workspace/ live under this
-- path instead, so an engagement's OpenSecBench files stay contained in a directory the user designates
-- (and a project delete removes only that directory).
ALTER TABLE project_index ADD COLUMN dir TEXT NOT NULL DEFAULT '';
