-- Tombstone for hard-deleted textures: the content goes, the moderation history
-- around it survives (reports keep pointing at texture_id). The hash stays so an
-- operator can tell whether the same bytes were uploaded again and can recover
-- the content from backup if deletion was a mistake.
CREATE TABLE texture_tombstones (
  texture_id  BIGINT NOT NULL PRIMARY KEY,
  uid         BIGINT NOT NULL,
  type        VARCHAR(6) NOT NULL CHECK (type IN ('skin','cape')),
  hash        VARCHAR(64) NOT NULL,
  model       VARCHAR(6) NULL CHECK (model IN ('default','slim') OR model IS NULL),
  description VARCHAR(255) NULL,
  source      VARCHAR(16) NOT NULL DEFAULT '',
  source_url  VARCHAR(512) NULL,
  public      SMALLINT NOT NULL DEFAULT 1,
  moderation  VARCHAR(16) NOT NULL DEFAULT '',
  upload_at   TIMESTAMPTZ NOT NULL,
  deleted_by  BIGINT NOT NULL,
  deleted_at  TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  reason      VARCHAR(500) NOT NULL DEFAULT ''
);
CREATE INDEX idx_tombstone_hash ON texture_tombstones (hash);
