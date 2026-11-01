-- Web session tokens (opaque, httpOnly cookie). ygg_tokens stays Yggdrasil-only.
CREATE TABLE web_sessions (
  token      CHAR(64) NOT NULL,
  user_id    BIGINT UNSIGNED NOT NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  expires_at DATETIME(3) NOT NULL,
  PRIMARY KEY (token),
  KEY idx_web_sessions_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
