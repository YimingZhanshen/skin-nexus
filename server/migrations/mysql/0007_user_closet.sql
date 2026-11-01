-- Closet (data-compat rule 7): 1:1 legacy user_closet (2019_03_14 create:
-- user_uid, texture_tid, item_name TEXT NULL — no PK, no unique constraint,
-- so legacy duplicate rows stay copyable). Columns widened to our BIGINT
-- user/texture ids (value-compatible); surrogate PK + user index added for ops
-- only — no unique key, the app dedupes (legacy had none either).
CREATE TABLE IF NOT EXISTS user_closet (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_uid    BIGINT UNSIGNED NOT NULL,
  texture_tid BIGINT UNSIGNED NOT NULL,
  item_name   TEXT NULL,
  PRIMARY KEY (id),
  KEY idx_user_closet_user (user_uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
