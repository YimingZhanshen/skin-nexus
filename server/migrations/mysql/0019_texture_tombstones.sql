-- Tombstone for hard-deleted textures. Deletion must be able to remove the
-- content (file + row) while the moderation history around it survives: reports
-- keep pointing at texture_id, and the panel still has to render what was taken
-- down, by whom, and why. audit_log cannot carry that — it has no index by
-- texture or hash, is a human-readable rolling stream, and cannot be joined.
-- The hash stays here so an operator can tell whether the same bytes were
-- uploaded again, and can recover the content from backup if it was removed in
-- error.
CREATE TABLE texture_tombstones (
  texture_id  BIGINT UNSIGNED PRIMARY KEY,
  uid         BIGINT UNSIGNED NOT NULL,
  type        ENUM('skin','cape') NOT NULL,
  hash        CHAR(64) NOT NULL,
  model       ENUM('default','slim') NULL,
  description VARCHAR(255) NULL,
  source      VARCHAR(16) NOT NULL DEFAULT '',
  source_url  VARCHAR(512) NULL,
  public      TINYINT(1) NOT NULL DEFAULT 1,
  moderation  VARCHAR(16) NOT NULL DEFAULT '',
  upload_at   DATETIME(3) NOT NULL,
  deleted_by  BIGINT UNSIGNED NOT NULL,
  deleted_at  DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  reason      VARCHAR(500) NOT NULL DEFAULT '',
  KEY idx_tombstone_hash (hash)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
