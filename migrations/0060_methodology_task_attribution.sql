-- Mirrors project/0012_methodology_task_attribution.sql for the legacy single-database layout (tests +
-- combined mode). Attributes a task to the methodology item + run that spawned it (ADR-0056).
ALTER TABLE tasks ADD COLUMN methodology_item_id TEXT;
ALTER TABLE tasks ADD COLUMN methodology_run_id TEXT;
