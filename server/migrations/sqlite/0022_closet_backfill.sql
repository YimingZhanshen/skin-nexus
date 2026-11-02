-- Closet backfill: the closet IS the set a player may wear, so a texture must
-- sit in it to be wearable — one's own uploads included. Upload auto-collects
-- from now on; this backfills the rows that predate that rule (and the ones a
-- Blessing import carried over, which never collected on upload).
--
-- Kept deliberately idempotent: the LEFT JOIN anti-match inserts a pair only
-- when nothing matches it yet, so a re-run on a pre-tracking database (which
-- re-applies unmarked files) is a no-op instead of a duplicate. That is the
-- condition under which this tree holds a data statement at all.
--
-- Textures whose uploader no longer exists are skipped: the account row is
-- gone, so a closet entry for it would be unreachable junk.
INSERT INTO user_closet (user_uid, texture_tid, item_name)
SELECT t.uid, t.id, NULL
  FROM textures t
  JOIN users u ON u.id = t.uid
  LEFT JOIN user_closet c ON c.user_uid = t.uid AND c.texture_tid = t.id
 WHERE c.id IS NULL;
