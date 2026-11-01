-- Web session tokens (opaque, httpOnly cookie). ygg_tokens stays Yggdrasil-only.
CREATE TABLE web_sessions (
  token      CHAR(64) NOT NULL PRIMARY KEY,
  user_id    INTEGER NOT NULL,
  created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now')),
  expires_at DATETIME NOT NULL
);
CREATE INDEX idx_web_sessions_user ON web_sessions (user_id);
