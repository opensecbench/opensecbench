-- A thread's agent profile (ADR-0019): which specialized agent drives it — its persona and its
-- least-privilege tool allow-list. Defaults to 'generalist' (the full toolset, today's behaviour), so
-- existing threads are unchanged.
ALTER TABLE threads ADD COLUMN agent_type TEXT NOT NULL DEFAULT 'generalist';
