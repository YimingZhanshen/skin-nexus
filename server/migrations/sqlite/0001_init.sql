-- SkinNexus initial schema (SQLite, modernc.org/sqlite — no CGO).
-- Same shape as migrations/mysql/0001_init.sql; only the dialect differs:
--   * time columns are declared DATETIME on purpose — the driver converts a
--     parseable value back into time.Time only for a timestamp-ish declared
--     type, and a TEXT-declared column comes back as a string.
--   * defaults are UTC by construction ('now' is UTC) and carry an explicit
--     +0000 offset, so they order identically with values written by Go.
--     dbx rewrites every time.Time argument to UTC first: SQLite stores times
--     as text, and that text only orders like time while every value shares
--     one offset.
--   * ON UPDATE CURRENT_TIMESTAMP does not exist here; writers set updated_at
--     themselves.
--   * the case-folding uniques become LOWER() expression indexes — MySQL's
--     utf8mb4_general_ci folded email, nickname and player name implicitly.

CREATE TABLE users (
  id            INTEGER PRIMARY KEY,
  email         VARCHAR(100) NULL,
  nickname      VARCHAR(50)  NOT NULL,
  password      VARCHAR(255) NOT NULL,
  password_algo VARCHAR(32)  NOT NULL DEFAULT 'argon2id',
  player_limit  INTEGER NOT NULL DEFAULT 3,
  ip            VARCHAR(45) NULL,
  avatar_tid    INTEGER NULL,
  created_at    DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now')),
  updated_at    DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now'))
);
CREATE UNIQUE INDEX uq_users_email ON users (LOWER(email));
CREATE UNIQUE INDEX uq_users_nickname ON users (LOWER(nickname));

CREATE TABLE players (
  id          INTEGER PRIMARY KEY,
  uid         INTEGER NOT NULL,
  name        VARCHAR(16) NOT NULL,
  tid_skin    INTEGER NULL,
  tid_cape    INTEGER NULL,
  preference  VARCHAR(10) NULL,
  created_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now')),
  updated_at  DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now'))
);
CREATE UNIQUE INDEX uq_players_name ON players (LOWER(name));
CREATE INDEX idx_players_uid ON players (uid);

CREATE TABLE textures (
  id          INTEGER PRIMARY KEY,
  uid         INTEGER NOT NULL,
  type        TEXT NOT NULL CHECK (type IN ('skin','cape')),
  hash        CHAR(64) NOT NULL,
  model       TEXT NULL CHECK (model IN ('default','slim') OR model IS NULL),
  description VARCHAR(255) NULL,
  upload_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now'))
);
CREATE INDEX idx_textures_uid_type ON textures (uid, type);
CREATE INDEX idx_textures_hash ON textures (hash);

CREATE TABLE uuid_map (
  name      VARCHAR(16) NOT NULL PRIMARY KEY,
  uuid      CHAR(32)    NOT NULL,
  player_id INTEGER NULL
);
CREATE UNIQUE INDEX uq_uuid_map_uuid ON uuid_map (uuid);
CREATE INDEX idx_uuid_map_player ON uuid_map (player_id);

CREATE TABLE options (
  k VARCHAR(100) NOT NULL PRIMARY KEY,
  v TEXT NULL
);

CREATE TABLE ygg_tokens (
  id           INTEGER PRIMARY KEY,
  user_id      INTEGER NOT NULL,
  client_token CHAR(32) NOT NULL,
  access_token CHAR(64) NOT NULL,
  selected_pid INTEGER NULL,
  created_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now')),
  expires_at   DATETIME NOT NULL,
  invalidated  INTEGER NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX uq_ygg_tokens_access ON ygg_tokens (access_token);
CREATE INDEX idx_ygg_tokens_user ON ygg_tokens (user_id);

CREATE TABLE ygg_join_sessions (
  id           INTEGER PRIMARY KEY,
  access_token CHAR(64) NOT NULL,
  uuid         CHAR(32) NOT NULL,
  server_id    VARCHAR(64) NULL,
  server_ip    VARCHAR(45) NULL,
  created_at   DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now')),
  expires_at   DATETIME NOT NULL
);
CREATE INDEX idx_join_token ON ygg_join_sessions (access_token);

CREATE TABLE audit_log (
  id         INTEGER PRIMARY KEY,
  actor_uid  INTEGER NULL,
  action     VARCHAR(64) NOT NULL,
  detail     TEXT NULL,
  created_at DATETIME NOT NULL DEFAULT (strftime('%Y-%m-%d %H:%M:%f +0000 UTC','now'))
);
CREATE INDEX idx_audit_actor ON audit_log (actor_uid);
CREATE INDEX idx_audit_action ON audit_log (action);
