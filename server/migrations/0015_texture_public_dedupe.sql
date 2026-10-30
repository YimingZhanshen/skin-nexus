-- Public texture content dedupe.
--
-- public_dedupe is type:sha256 for public rows and NULL for private rows.
-- A unique index therefore permits many private copies but only one public
-- row per (type,hash). Existing legacy duplicates keep NULL until the Go
-- backfill picks one winner per group; duplicates are not deleted.
ALTER TABLE textures ADD COLUMN public_dedupe VARCHAR(70) NULL;
CREATE UNIQUE INDEX uk_textures_public_dedupe ON textures (public_dedupe);
