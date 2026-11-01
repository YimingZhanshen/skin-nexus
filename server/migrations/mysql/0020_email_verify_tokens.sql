-- Email verification tokens (2026-11-02): same shape as password_reset_tokens
-- (0010) because the threat model is identical — a bearer link that must be
-- single-use, short-lived, and unreadable if the DB leaks.
--   * only the SHA-256 hex digest is stored;
--   * write and validate on the same DB clock (NOW(3)) so Go/MySQL timezone
--     drift can never lengthen a link's life;
--   * a new request marks that uid's unused tokens used, so the last email
--     sent is the only one that works;
--   * no FK to users: the account can be deleted while a link is still live,
--     and confirming then simply finds no user.
CREATE TABLE IF NOT EXISTS email_verify_tokens (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  uid        BIGINT UNSIGNED NOT NULL,
  token_hash CHAR(64)        NOT NULL,
  expires_at DATETIME(3)     NOT NULL,
  used_at    DATETIME(3)     NULL,
  created_at DATETIME(3)     NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_evt_token (token_hash),
  KEY idx_evt_uid (uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
