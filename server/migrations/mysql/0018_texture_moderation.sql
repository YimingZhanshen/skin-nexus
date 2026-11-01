-- Per-texture moderation state. A texture judged to have violated the rules on
-- a report can be pulled from the library, and must not be able to walk back in
-- by its owner flipping visibility. The verdict belongs on the texture, not on
-- the report row: one texture accumulates many reports over time, and the last
-- one to be handled would otherwise silently overwrite the previous verdict.
-- '' = clean, 'violation' = struck (may stay private, may not be republished).
ALTER TABLE textures ADD COLUMN moderation VARCHAR(16) NOT NULL DEFAULT '';
