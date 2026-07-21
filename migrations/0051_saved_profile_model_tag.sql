-- ADR-0052 (legacy combined schema mirror of global/0003): custom agents carry a routing tag.
ALTER TABLE saved_profiles ADD COLUMN model_tag TEXT NOT NULL DEFAULT '';;
