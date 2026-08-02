-- Per-destination data clearance (governance rework): the highest asset-sensitivity tier a connection
-- (or a specific model it serves) is approved to receive over external LLM egress. This replaces the
-- global personal/corporate/strict governance profiles — egress is now decided per destination.
--
-- New connections default to the least-privilege 'open_source'; existing rows migrate to it and must be
-- explicitly re-approved for a higher tier. A per-model override of '' means "inherit the connection".
ALTER TABLE providers ADD COLUMN data_clearance TEXT NOT NULL DEFAULT 'open_source';;
ALTER TABLE providers ADD COLUMN clearance_note TEXT NOT NULL DEFAULT '';;

ALTER TABLE connection_models ADD COLUMN data_clearance TEXT NOT NULL DEFAULT '';;
ALTER TABLE connection_models ADD COLUMN clearance_note TEXT NOT NULL DEFAULT '';;
