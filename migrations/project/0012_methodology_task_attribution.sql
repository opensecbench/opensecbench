-- Attribute a task to the methodology item + run that spawned it (ADR-0056), so when the task completes the
-- engine's on-complete hook can attach its observations to that item as evidence and flip the item's coverage.
-- Both nullable: an ordinary scan/agent task carries neither.
ALTER TABLE tasks ADD COLUMN methodology_item_id TEXT;
ALTER TABLE tasks ADD COLUMN methodology_run_id TEXT;
