-- Texture metadata: long Markdown description + provenance + moderation reports.
-- description keeps its existing meaning (the short display name).
ALTER TABLE textures
  ADD COLUMN detail_md  TEXT NULL,
  ADD COLUMN source     VARCHAR(16) NOT NULL DEFAULT '',
  ADD COLUMN source_url VARCHAR(512) NULL;

CREATE TABLE texture_reports (
  id           BIGINT UNSIGNED PRIMARY KEY AUTO_INCREMENT,
  texture_id   BIGINT UNSIGNED NOT NULL,
  reporter_uid BIGINT UNSIGNED NOT NULL,
  reason       VARCHAR(16) NOT NULL,
  detail       VARCHAR(500) NULL,
  status       VARCHAR(16) NOT NULL DEFAULT 'open',
  handled_by   BIGINT UNSIGNED NULL,
  handled_at   DATETIME(3) NULL,
  created_at   DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  UNIQUE KEY uk_report_user_texture (texture_id, reporter_uid),
  KEY idx_report_status_time (status, created_at),
  KEY idx_report_texture (texture_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
