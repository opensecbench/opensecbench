-- 0011_playbook_max_concurrency: per-playbook concurrency cap. Overrides the global default
-- (OSB_PLAN_MAX_PARALLEL, default 4) so a playbook author can widen or narrow parallelism.
-- 0 means "use the global default."
ALTER TABLE saved_playbooks ADD COLUMN max_concurrency INTEGER NOT NULL DEFAULT 0;
