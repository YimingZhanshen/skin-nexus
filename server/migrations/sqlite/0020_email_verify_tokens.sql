-- Email verification tokens: identical shape to password_reset_tokens (0010)
-- because the threat model is identical — a bearer link that must be
-- single-use, short-lived and unreadable if the database leaks. No FK to
-- users: the account can be deleted while a link is still live.
CREATE TABLE IF NOT EXISTS email_verify_tokens (
  id         INTEGER PRIMARY KEY,
  uid        INTEGER NOT NULL,
  token_hash CHAR(64) NOT NULL,
  expires_at DATETIME NOT NULL,
  used_at    DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now'))
);
CREATE INDEX idx_evt_token ON email_verify_tokens (token_hash);
CREATE INDEX idx_evt_uid ON email_verify_tokens (uid);
