-- 0020_task_project: associate a task with a project directly, so network/SCA capabilities (which
-- run with a project scope but no application) still roll up into project graphs and reports.

ALTER TABLE tasks ADD COLUMN project_id TEXT REFERENCES projects(id);
CREATE INDEX idx_tasks_project ON tasks(project_id);
