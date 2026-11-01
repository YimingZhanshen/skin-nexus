-- Public texture content dedupe: type:sha256 for public rows, NULL for private
-- ones, so the unique index permits many private copies but only one public row
-- per content (PG treats NULLs as distinct, same as MySQL). Legacy duplicates
-- keep NULL until the Go backfill picks one winner per group.
ALTER TABLE textures ADD COLUMN public_dedupe VARCHAR(70) NULL;
CREATE UNIQUE INDEX uk_textures_public_dedupe ON textures (public_dedupe);
