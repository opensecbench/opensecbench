-- Mirrors project/0013_methodology_task_items.sql for the legacy single-database layout (tests + combined
-- mode). A methodology task serves several items at once (ADR-0056); item ids stored as a JSON array.
ALTER TABLE tasks ADD COLUMN methodology_item_ids TEXT;
