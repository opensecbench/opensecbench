-- ADR-0043: knowledge freshness. Track when each fact was last affirmatively verified so accumulated
-- knowledge doesn't silently rot — old-but-confirmed entries go stale in the dossier and can be re-verified.
ALTER TABLE kb_entries ADD COLUMN last_verified_at TEXT;

-- Backfill: an already-confirmed entry is treated as verified as of its last update; unreviewed drafts and
-- rejected entries stay unverified (NULL) — they've never been affirmatively checked.
UPDATE kb_entries SET last_verified_at = updated_at WHERE review_state = 'confirmed';
