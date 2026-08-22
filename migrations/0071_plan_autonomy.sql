-- 0071_plan_autonomy: conditional plan steps + per-playbook concurrency cap.
ALTER TABLE plan_steps ADD COLUMN skip_if TEXT NOT NULL DEFAULT '';
ALTER TABLE saved_playbooks ADD COLUMN max_concurrency INTEGER NOT NULL DEFAULT 0;
