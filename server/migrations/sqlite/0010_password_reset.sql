-- Password reset tokens: the raw token never touches disk (SHA-256 hex digest
-- only), single use is enforced by the atomic consume UPDATE, and the TTL
-- deadline is written and validated on one clock. SQLite has no NOW(3) — the
-- deadline is compared against a UTC value in exactly the shape dbx writes.
CREATE TABLE IF NOT EXISTS password_reset_tokens (
  id         INTEGER PRIMARY KEY,
  uid        INTEGER NOT NULL,
  token_hash CHAR(64) NOT NULL,
  expires_at DATETIME NOT NULL,
  used_at    DATETIME NULL,
  created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now'))
);
CREATE INDEX idx_prt_token ON password_reset_tokens (token_hash);
CREATE INDEX idx_prt_uid ON password_reset_tokens (uid);
