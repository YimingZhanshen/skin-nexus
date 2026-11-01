-- OIDC provider persistence (authorization-code + device flow + token ledgers).
-- Contract: server/internal/oidc/backend.go (record shapes frozen there).
-- Conventions: UTC DATETIME(3) + parseTime; utf8mb4; InnoDB; zero times are
-- stored as NULL so strict sql_mode never sees a zero date.

-- Registered OAuth clients. The shared public client used by launchers
-- (discovery `shared_client_id`) is a row with is_public = 1 and empty secret.
CREATE TABLE oidc_clients (
  client_id     VARCHAR(128) NOT NULL,
  secret_hash   VARCHAR(255) NOT NULL DEFAULT '',   -- '' for public clients
  name          VARCHAR(100) NOT NULL,
  redirect_uris TEXT NOT NULL,                      -- JSON array of strings
  grant_types   TEXT NOT NULL,                      -- JSON array of strings
  scopes        TEXT NOT NULL,                      -- JSON array; [] = all scopes
  is_public     TINYINT(1) NOT NULL DEFAULT 0,
  created_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (client_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Authorization-code flow (P1) incl. PKCE. code is UNIQUE but nullable: rows
-- live code-less until the code is minted at the consent step.
CREATE TABLE oidc_auth_requests (
  id                    VARCHAR(128) NOT NULL,
  client_id             VARCHAR(128) NOT NULL,
  subject               VARCHAR(64)  NOT NULL DEFAULT '',  -- uid decimal string, set at login
  scopes                TEXT NOT NULL,
  redirect_uri          VARCHAR(1024) NOT NULL DEFAULT '',
  state                 VARCHAR(512) NOT NULL DEFAULT '',
  nonce                 VARCHAR(255) NOT NULL DEFAULT '',
  code_challenge        VARCHAR(128) NOT NULL DEFAULT '',
  code_challenge_method VARCHAR(10)  NOT NULL DEFAULT '',  -- 'S256'; plain rejected by policy
  selected_pid          BIGINT UNSIGNED NULL,
  auth_time             DATETIME(3) NULL,
  done                  TINYINT(1) NOT NULL DEFAULT 0,
  code                  VARCHAR(128) NULL,
  code_expires          DATETIME(3) NULL,
  created_at            DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_oidc_auth_requests_code (code)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- JWT access-token ledger (jti). revoked_at NULL = live; expiry is enforced by
-- the caller against expires_at, revocation on every Bearer verification.
CREATE TABLE oidc_access_tokens (
  jti          VARCHAR(64) NOT NULL,
  uid          BIGINT UNSIGNED NOT NULL,
  client_id    VARCHAR(128) NOT NULL,
  scopes       TEXT NOT NULL,
  selected_pid BIGINT UNSIGNED NULL,
  expires_at   DATETIME(3) NOT NULL,
  created_at   DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  revoked_at   DATETIME(3) NULL,
  PRIMARY KEY (jti),
  KEY idx_oidc_access_tokens_uid (uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Opaque refresh tokens. Rotation marks the old row revoked and inserts the
-- new one in a single transaction; token strings never appear in logs.
CREATE TABLE oidc_refresh_tokens (
  token        VARCHAR(128) NOT NULL,
  uid          BIGINT UNSIGNED NOT NULL,
  client_id    VARCHAR(128) NOT NULL,
  scopes       TEXT NOT NULL,
  selected_pid BIGINT UNSIGNED NULL,
  auth_time    DATETIME(3) NULL,
  expires_at   DATETIME(3) NOT NULL,
  created_at   DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  revoked      TINYINT(1) NOT NULL DEFAULT 0,
  PRIMARY KEY (token),
  KEY idx_oidc_refresh_tokens_uid (uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- RFC 8628 device authorizations. done/denied are flipped by the site's
-- verification page (login + consent + profile select); uid stays 0 until
-- approval. user_code is regenerated on collision (ErrDuplicateUserCode).
CREATE TABLE oidc_device_auth (
  device_code  VARCHAR(64) NOT NULL,
  user_code    VARCHAR(16) NOT NULL,               -- XXXX-XXXX, base20 charset
  client_id    VARCHAR(128) NOT NULL,
  scopes       TEXT NOT NULL,
  expires_at   DATETIME(3) NOT NULL,
  done         TINYINT(1) NOT NULL DEFAULT 0,
  denied       TINYINT(1) NOT NULL DEFAULT 0,
  uid          BIGINT UNSIGNED NOT NULL DEFAULT 0, -- 0 = not approved yet
  selected_pid BIGINT UNSIGNED NULL,
  auth_time    DATETIME(3) NULL,
  created_at   DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (device_code),
  UNIQUE KEY uq_oidc_device_auth_user_code (user_code),
  KEY idx_oidc_device_auth_user_code (user_code)   -- per design spec; uq above already covers it
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
