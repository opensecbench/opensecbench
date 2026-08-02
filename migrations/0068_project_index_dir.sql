-- 0068_project_index_dir: mirror of global/0010 for the root/combined schema (tests). Custom project
-- location is only honored in split mode; the column exists in combined so the index code paths are uniform.
ALTER TABLE project_index ADD COLUMN dir TEXT NOT NULL DEFAULT '';
