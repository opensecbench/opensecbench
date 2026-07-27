-- A methodology task can serve several items at once (ADR-0056): when multiple items map to the same
-- capability, the capability runs ONCE and its results attach to every requesting item — dedup + correct
-- multi-item evidence (a single fingerprint-deduped observation set would otherwise land on only the first
-- item). Stores the item ids as a JSON array; supersedes the single methodology_item_id column (0012).
ALTER TABLE tasks ADD COLUMN methodology_item_ids TEXT;
