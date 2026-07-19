-- 0032_usage_agent_type: attribute each Analyst run's token usage to the agent profile (agent_type)
-- that incurred it, so spend can be broken down per-agent — not just per provider/model. Existing rows
-- default to '' (unattributed).

ALTER TABLE usage_records ADD COLUMN agent_type TEXT NOT NULL DEFAULT '';
