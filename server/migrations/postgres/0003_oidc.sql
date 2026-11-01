-- OIDC provider persistence (authorization-code + device flow + token ledgers).
-- Contract: server/internal/oidc/backend.go (record shapes frozen there).
-- JSON arrays stay TEXT: the Go layer marshals them, so no engine-side JSON
-- operator is depended on anywhere.

CREATE TABLE oidc_clients (
  client_id     VARCHAR(128) NOT NULL PRIMARY KEY,
  secret_hash   VARCHAR(255) NOT NULL DEFAULT '',
  name          VARCHAR(100) NOT NULL,
  redirect_uris TEXT NOT NULL,
  grant_types   TEXT NOT NULL,
  scopes        TEXT NOT NULL,
  is_public     SMALLINT NOT NULL DEFAULT 0,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE oidc_auth_requests (
  id                    VARCHAR(128) NOT NULL PRIMARY KEY,
  client_id             VARCHAR(128) NOT NULL,
  subject               VARCHAR(64)  NOT NULL DEFAULT '',
  scopes                TEXT NOT NULL,
  redirect_uri          VARCHAR(1024) NOT NULL DEFAULT '',
  state                 VARCHAR(512) NOT NULL DEFAULT '',
  nonce                 VARCHAR(255) NOT NULL DEFAULT '',
  code_challenge        VARCHAR(128) NOT NULL DEFAULT '',
  code_challenge_method VARCHAR(10)  NOT NULL DEFAULT '',
  selected_pid          BIGINT NULL,
  auth_time             TIMESTAMPTZ NULL,
  done                  SMALLINT NOT NULL DEFAULT 0,
  code                  VARCHAR(128) NULL,
  code_expires          TIMESTAMPTZ NULL,
  created_at            TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX uq_oidc_auth_requests_code ON oidc_auth_requests (code);

CREATE TABLE oidc_access_tokens (
  jti          VARCHAR(64) NOT NULL PRIMARY KEY,
  uid          BIGINT NOT NULL,
  client_id    VARCHAR(128) NOT NULL,
  scopes       TEXT NOT NULL,
  selected_pid BIGINT NULL,
  expires_at   TIMESTAMPTZ NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  revoked_at   TIMESTAMPTZ NULL
);
CREATE INDEX idx_oidc_access_tokens_uid ON oidc_access_tokens (uid);

CREATE TABLE oidc_refresh_tokens (
  token        VARCHAR(128) NOT NULL PRIMARY KEY,
  uid          BIGINT NOT NULL,
  client_id    VARCHAR(128) NOT NULL,
  scopes       TEXT NOT NULL,
  selected_pid BIGINT NULL,
  auth_time    TIMESTAMPTZ NULL,
  expires_at   TIMESTAMPTZ NOT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  revoked      SMALLINT NOT NULL DEFAULT 0
);
CREATE INDEX idx_oidc_refresh_tokens_uid ON oidc_refresh_tokens (uid);

CREATE TABLE oidc_device_auth (
  device_code  VARCHAR(64) NOT NULL PRIMARY KEY,
  user_code    VARCHAR(16) NOT NULL,
  client_id    VARCHAR(128) NOT NULL,
  scopes       TEXT NOT NULL,
  expires_at   TIMESTAMPTZ NOT NULL,
  done         SMALLINT NOT NULL DEFAULT 0,
  denied       SMALLINT NOT NULL DEFAULT 0,
  uid          BIGINT NOT NULL DEFAULT 0,
  selected_pid BIGINT NULL,
  auth_time    TIMESTAMPTZ NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX uq_oidc_device_auth_user_code ON oidc_device_auth (user_code);
CREATE INDEX idx_oidc_device_auth_user_code ON oidc_device_auth (user_code);
