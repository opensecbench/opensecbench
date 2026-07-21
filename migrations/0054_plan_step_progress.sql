-- 0054_plan_step_progress: live per-step activity trail (legacy set mirror of project/0007). See that file.
ALTER TABLE plan_steps ADD COLUMN progress TEXT NOT NULL DEFAULT '';
