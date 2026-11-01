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
  is_public     INTEGER NOT NULL DEFAULT 0,
  created_at    DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now'))
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
  selected_pid          INTEGER NULL,
  auth_time             DATETIME NULL,
  done                  INTEGER NOT NULL DEFAULT 0,
  code                  VARCHAR(128) NULL,
  code_expires          DATETIME NULL,
  created_at            DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now'))
);
CREATE UNIQUE INDEX uq_oidc_auth_requests_code ON oidc_auth_requests (code);

CREATE TABLE oidc_access_tokens (
  jti          VARCHAR(64) NOT NULL PRIMARY KEY,
  uid          INTEGER NOT NULL,
  client_id    VARCHAR(128) NOT NULL,
  scopes       TEXT NOT NULL,
  selected_pid INTEGER NULL,
  expires_at   DATETIME NOT NULL,
  created_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now')),
  revoked_at   DATETIME NULL
);
CREATE INDEX idx_oidc_access_tokens_uid ON oidc_access_tokens (uid);

CREATE TABLE oidc_refresh_tokens (
  token        VARCHAR(128) NOT NULL PRIMARY KEY,
  uid          INTEGER NOT NULL,
  client_id    VARCHAR(128) NOT NULL,
  scopes       TEXT NOT NULL,
  selected_pid INTEGER NULL,
  auth_time    DATETIME NULL,
  expires_at   DATETIME NOT NULL,
  created_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now')),
  revoked      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_oidc_refresh_tokens_uid ON oidc_refresh_tokens (uid);

CREATE TABLE oidc_device_auth (
  device_code  VARCHAR(64) NOT NULL PRIMARY KEY,
  user_code    VARCHAR(16) NOT NULL,
  client_id    VARCHAR(128) NOT NULL,
  scopes       TEXT NOT NULL,
  expires_at   DATETIME NOT NULL,
  done         INTEGER NOT NULL DEFAULT 0,
  denied       INTEGER NOT NULL DEFAULT 0,
  uid          INTEGER NOT NULL DEFAULT 0,
  selected_pid INTEGER NULL,
  auth_time    DATETIME NULL,
  created_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now'))
);
CREATE UNIQUE INDEX uq_oidc_device_auth_user_code ON oidc_device_auth (user_code);
CREATE INDEX idx_oidc_device_auth_user_code ON oidc_device_auth (user_code);
