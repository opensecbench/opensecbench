-- 0010_asset_ecosystems: manual technology/ecosystem tags on an asset (python, go, rust, node, …).
-- The scan auto-run gate unions these with what it detects from marker files, so an operator can correct
-- a monorepo/polyglot repo that root-level detection under-reads. Comma-separated.
ALTER TABLE assets ADD COLUMN ecosystems TEXT NOT NULL DEFAULT '';
