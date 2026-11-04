-- The library's default view is「public, newest first」: WHERE public = 1
-- ORDER BY upload_at DESC, id DESC (internal/store/skinlib.go). Nothing indexed
-- the pair, so every first page sorted the largest table in the site.
--
-- What this buys and what it does not: on InnoDB a secondary index carries the
-- primary key, so (public, upload_at) really stores (public, upload_at, id) and a
-- backward scan yields both DESC columns in the order the query wants. The
-- sitemap query (public, ORDER BY id) is helped only in filtering — it still
-- sorts. That is a known limit of this index, not a second thing it does.
CREATE INDEX idx_textures_public_upload ON textures (public, upload_at);
