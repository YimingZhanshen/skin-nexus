-- Email verification tokens: identical shape to password_reset_tokens (0010)
-- because the threat model is identical — a bearer link that must be
-- single-use, short-lived and unreadable if the database leaks. No FK to
-- users: the account can be deleted while a link is still live, and confirming
-- then simply finds no user.
CREATE TABLE IF NOT EXISTS email_verify_tokens (
  id         BIGSERIAL PRIMARY KEY,
  uid        BIGINT NOT NULL,
  token_hash VARCHAR(64) NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  used_at    TIMESTAMPTZ NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_evt_token ON email_verify_tokens (token_hash);
CREATE INDEX idx_evt_uid ON email_verify_tokens (uid);
