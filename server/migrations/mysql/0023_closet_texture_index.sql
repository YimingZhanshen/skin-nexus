-- The library list asks two questions per visible texture: how many ♥ it has,
-- and whether the viewer already liked it. Both are keyed on texture_tid
-- (internal/store/skinlib.go), while this pivot only ever had an index leading
-- with the account — which answers "what is in my wardrobe" and nothing else.
-- Without this, a first-page render costs one full scan of the pivot per row.
CREATE INDEX idx_closet_texture ON user_closet (texture_tid, user_uid);
