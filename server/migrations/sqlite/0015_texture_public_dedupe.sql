-- Public texture content dedupe: type:sha256 for public rows, NULL for private
-- ones, so the unique index permits many private copies but one public row per
-- content. Legacy duplicates keep NULL until the Go backfill picks a winner.
ALTER TABLE textures ADD COLUMN public_dedupe VARCHAR(70) NULL;
CREATE UNIQUE INDEX uk_textures_public_dedupe ON textures (public_dedupe);
