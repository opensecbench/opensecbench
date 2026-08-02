-- Legacy combined-schema mirror of global/0008: per-destination data clearance. The highest asset
-- sensitivity tier a connection (or a specific model it serves) is approved to receive over external LLM
-- egress. Replaces the personal/corporate/strict governance profiles. New connections default to the
-- least-privilege 'open_source'; a per-model override of '' means "inherit the connection".
ALTER TABLE providers ADD COLUMN data_clearance TEXT NOT NULL DEFAULT 'open_source';;
ALTER TABLE providers ADD COLUMN clearance_note TEXT NOT NULL DEFAULT '';;

ALTER TABLE connection_models ADD COLUMN data_clearance TEXT NOT NULL DEFAULT '';;
ALTER TABLE connection_models ADD COLUMN clearance_note TEXT NOT NULL DEFAULT '';;
