-- Closet (data-compat rule 7): 1:1 legacy user_closet. Legacy had no PK and no
-- unique key, so duplicate rows stay copyable; the surrogate id and the user
-- index are ours for ops only.
CREATE TABLE IF NOT EXISTS user_closet (
  id          BIGSERIAL PRIMARY KEY,
  user_uid    BIGINT NOT NULL,
  texture_tid BIGINT NOT NULL,
  item_name   TEXT NULL
);
CREATE INDEX IF NOT EXISTS idx_user_closet_user ON user_closet (user_uid);
