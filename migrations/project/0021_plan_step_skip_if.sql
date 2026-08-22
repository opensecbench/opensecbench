-- 0021_plan_step_skip_if: conditional steps — the plan runner evaluates the condition before delegating;
-- if met, the step is skipped (treated as done so dependents proceed). Avoids burning tokens on a
-- sub-agent that would discover its precondition is unmet.
ALTER TABLE plan_steps ADD COLUMN skip_if TEXT NOT NULL DEFAULT '';
