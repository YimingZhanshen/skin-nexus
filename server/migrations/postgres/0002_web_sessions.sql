-- Web session tokens (opaque, httpOnly cookie). ygg_tokens stays Yggdrasil-only.
CREATE TABLE web_sessions (
  token      VARCHAR(64) NOT NULL PRIMARY KEY,
  user_id    BIGINT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_web_sessions_user ON web_sessions (user_id);
