-- 0058_context_tags: analyst tags + pin flag on context items (legacy set mirror of project/0011).
ALTER TABLE context_items ADD COLUMN tags TEXT NOT NULL DEFAULT '';
ALTER TABLE context_items ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0;
