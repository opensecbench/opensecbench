-- 0057_asset_ecosystems: manual ecosystem tags on assets (legacy set mirror of project/0010).
ALTER TABLE assets ADD COLUMN ecosystems TEXT NOT NULL DEFAULT '';
