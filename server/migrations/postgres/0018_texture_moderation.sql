-- Per-texture moderation state ('' = clean, 'violation' = struck). The verdict
-- belongs on the texture, not on the last-handled report row: one texture
-- accumulates many reports, and a struck texture may stay private but must not
-- be republished by its owner flipping visibility.
ALTER TABLE textures ADD COLUMN moderation VARCHAR(16) NOT NULL DEFAULT '';
