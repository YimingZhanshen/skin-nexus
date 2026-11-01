-- Texture metadata: long Markdown description + provenance + moderation
-- reports. description keeps its existing meaning (the short display name).
ALTER TABLE textures ADD COLUMN detail_md TEXT NULL;
ALTER TABLE textures ADD COLUMN source VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE textures ADD COLUMN source_url VARCHAR(512) NULL;

CREATE TABLE texture_reports (
  id           BIGSERIAL PRIMARY KEY,
  texture_id   BIGINT NOT NULL,
  reporter_uid BIGINT NOT NULL,
  reason       VARCHAR(16) NOT NULL,
  detail       VARCHAR(500) NULL,
  status       VARCHAR(16) NOT NULL DEFAULT 'open',
  handled_by   BIGINT NULL,
  handled_at   TIMESTAMPTZ NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX uk_report_user_texture ON texture_reports (texture_id, reporter_uid);
CREATE INDEX idx_report_status_time ON texture_reports (status, created_at);
CREATE INDEX idx_report_texture ON texture_reports (texture_id);
