-- 0007_plan_step_progress: a live activity trail per plan step. As a sub-agent runs, each tool turn (name,
-- args summary, result/error) is appended here so the UI can stream what the step is actually doing and so a
-- failed step (e.g. hit its turn cap) is diagnosable after the fact. Empty until the step runs.
ALTER TABLE plan_steps ADD COLUMN progress TEXT NOT NULL DEFAULT '';
