-- 0011_context_tags: analyst-applied tags + a pin flag on context items (notes especially).
-- Tags are comma-separated free-form labels; a small reserved set (out-of-scope, constraint, priority,
-- hypothesis) is behavioral — the analyst agent acts on them. Pinned (or behaviorally-tagged) items are
-- auto-injected into the agent's run-start context; everything else stays pull-only via list_context.
ALTER TABLE context_items ADD COLUMN tags TEXT NOT NULL DEFAULT '';
ALTER TABLE context_items ADD COLUMN pinned INTEGER NOT NULL DEFAULT 0;
