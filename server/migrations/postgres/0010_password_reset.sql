-- Password reset tokens: the raw token never touches disk (SHA-256 hex digest
-- only), single use is enforced by the atomic consume UPDATE, and the TTL
-- deadline is written and validated on one clock (NOW() on PG).
CREATE TABLE IF NOT EXISTS password_reset_tokens (
  id         BIGSERIAL PRIMARY KEY,
  uid        BIGINT NOT NULL,
  token_hash VARCHAR(64) NOT NULL,
  expires_at TIMESTAMPTZ NOT NULL,
  used_at    TIMESTAMPTZ NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_prt_token ON password_reset_tokens (token_hash);
CREATE INDEX idx_prt_uid ON password_reset_tokens (uid);
