-- Password reset tokens (A1 找回密码, 2026-10-26):
--   * the raw token NEVER touches disk — only its SHA-256 hex digest (a DB
--     leak must not yield usable reset links);
--   * single-use is enforced by the atomic consume UPDATE (used_at IS NULL);
--   * the TTL deadline is written and compared with the SAME DB clock
--     (DATE_ADD(NOW(3) …) on write, NOW(3) on validate) so Go/MySQL timezone
--     drift can never extend or shorten a link's life;
--   * no FK to users: tokens expire in minutes anyway, and audit-style rows
--     surviving an account deletion costs nothing (reset then finds no user).
CREATE TABLE IF NOT EXISTS password_reset_tokens (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  uid        BIGINT UNSIGNED NOT NULL,
  token_hash CHAR(64)        NOT NULL,
  expires_at DATETIME(3)     NOT NULL,
  used_at    DATETIME(3)     NULL,
  created_at DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_prt_token (token_hash),
  KEY idx_prt_uid (uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
