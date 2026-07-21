-- ADR-0052: a custom agent is a task type, so it can carry a routing tag (empty = the default list).
ALTER TABLE saved_profiles ADD COLUMN model_tag TEXT NOT NULL DEFAULT '';;
