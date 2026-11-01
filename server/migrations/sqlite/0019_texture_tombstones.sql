-- Tombstone for hard-deleted textures: the content goes, the moderation
-- history around it survives (reports keep pointing at texture_id). The hash
-- stays so an operator can tell whether the same bytes came back and can
-- recover the content from backup if deletion was a mistake.
CREATE TABLE texture_tombstones (
  texture_id  INTEGER NOT NULL PRIMARY KEY,
  uid         INTEGER NOT NULL,
  type        TEXT NOT NULL CHECK (type IN ('skin','cape')),
  hash        CHAR(64) NOT NULL,
  model       TEXT NULL CHECK (model IN ('default','slim') OR model IS NULL),
  description VARCHAR(255) NULL,
  source      VARCHAR(16) NOT NULL DEFAULT '',
  source_url  VARCHAR(512) NULL,
  public      INTEGER NOT NULL DEFAULT 1,
  moderation  VARCHAR(16) NOT NULL DEFAULT '',
  upload_at   DATETIME NOT NULL,
  deleted_by  INTEGER NOT NULL,
  deleted_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now')),
  reason      VARCHAR(500) NOT NULL DEFAULT ''
);
CREATE INDEX idx_tombstone_hash ON texture_tombstones (hash);
