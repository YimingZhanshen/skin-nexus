-- Texture metadata: long Markdown description + provenance + moderation
-- reports. description keeps its existing meaning (the short display name).
ALTER TABLE textures ADD COLUMN detail_md TEXT NULL;
ALTER TABLE textures ADD COLUMN source VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE textures ADD COLUMN source_url VARCHAR(512) NULL;

CREATE TABLE texture_reports (
  id           INTEGER PRIMARY KEY,
  texture_id   INTEGER NOT NULL,
  reporter_uid INTEGER NOT NULL,
  reason       VARCHAR(16) NOT NULL,
  detail       VARCHAR(500) NULL,
  status       VARCHAR(16) NOT NULL DEFAULT 'open',
  handled_by   INTEGER NULL,
  handled_at   DATETIME NULL,
  created_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now'))
);
CREATE UNIQUE INDEX uk_report_user_texture ON texture_reports (texture_id, reporter_uid);
CREATE INDEX idx_report_status_time ON texture_reports (status, created_at);
CREATE INDEX idx_report_texture ON texture_reports (texture_id);
