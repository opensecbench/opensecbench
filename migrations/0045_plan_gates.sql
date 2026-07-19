-- ADR-0044: mid-run plan approval. A plan step can be a human-approval gate — once its dependencies
-- complete the plan pauses (status 'waiting') until a human approves it, then resumes. `gate` marks the
-- checkpoint step; `gate_approved` records that a human cleared it so a resumed run doesn't pause again.
ALTER TABLE plan_steps ADD COLUMN gate INTEGER NOT NULL DEFAULT 0;
ALTER TABLE plan_steps ADD COLUMN gate_approved INTEGER NOT NULL DEFAULT 0;
