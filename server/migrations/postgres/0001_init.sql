-- SkinNexus initial schema (PostgreSQL).
-- Same shape as migrations/mysql/0001_init.sql; the dialect differences:
--   * TIMESTAMPTZ everywhere: instants compare as instants, so the UTC-vs-
--     local hazard that SQLite's text storage has does not exist here.
--   * CHAR(n) is deliberately avoided: `char(n)` is blank-padded in PG, so
--     fixed-width hashes/tokens become VARCHAR.
--   * TINYINT(1) becomes SMALLINT and JSON/MEDIUMTEXT become TEXT — the host
--     marshals JSON in Go and no engine-side JSON operator is depended on.
--   * BIGSERIAL keeps its own sequence, so an import that inserts explicit
--     ids must resync it (see the importer notes in internal/migrate).
--   * there is no ON UPDATE CURRENT_TIMESTAMP; writers set updated_at.
--   * the case-folding uniques become LOWER() expression indexes: MySQL's
--     utf8mb4_general_ci folded email, nickname and player name implicitly.

CREATE TABLE users (
  id            BIGSERIAL PRIMARY KEY,
  email         VARCHAR(100) NULL,
  nickname      VARCHAR(50)  NOT NULL,
  password      VARCHAR(255) NOT NULL,
  password_algo VARCHAR(32)  NOT NULL DEFAULT 'argon2id',
  player_limit  INTEGER      NOT NULL DEFAULT 3,
  ip            VARCHAR(45)  NULL,
  avatar_tid    BIGINT       NULL,
  created_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at    TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX uq_users_email ON users (LOWER(email));
CREATE UNIQUE INDEX uq_users_nickname ON users (LOWER(nickname));

CREATE TABLE players (
  id         BIGSERIAL PRIMARY KEY,
  uid        BIGINT NOT NULL,
  name       VARCHAR(16) NOT NULL,
  tid_skin   BIGINT NULL,
  tid_cape   BIGINT NULL,
  preference VARCHAR(10) NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX uq_players_name ON players (LOWER(name));
CREATE INDEX idx_players_uid ON players (uid);

CREATE TABLE textures (
  id          BIGSERIAL PRIMARY KEY,
  uid         BIGINT NOT NULL,
  type        VARCHAR(10) NOT NULL CHECK (type IN ('skin','cape')),
  hash        VARCHAR(64) NOT NULL,
  model       VARCHAR(10) NULL CHECK (model IN ('default','slim') OR model IS NULL),
  description VARCHAR(255) NULL,
  upload_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_textures_uid_type ON textures (uid, type);
CREATE INDEX idx_textures_hash ON textures (hash);

CREATE TABLE uuid_map (
  name      VARCHAR(16) NOT NULL PRIMARY KEY,
  uuid      VARCHAR(32) NOT NULL,
  player_id BIGINT NULL
);
CREATE UNIQUE INDEX uq_uuid_map_uuid ON uuid_map (uuid);
CREATE INDEX idx_uuid_map_player ON uuid_map (player_id);

CREATE TABLE options (
  k VARCHAR(100) NOT NULL PRIMARY KEY,
  v TEXT NULL
);

CREATE TABLE ygg_tokens (
  id           BIGSERIAL PRIMARY KEY,
  user_id      BIGINT NOT NULL,
  client_token VARCHAR(32) NOT NULL,
  access_token VARCHAR(64) NOT NULL,
  selected_pid BIGINT NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at   TIMESTAMPTZ NOT NULL,
  invalidated  SMALLINT NOT NULL DEFAULT 0
);
CREATE UNIQUE INDEX uq_ygg_tokens_access ON ygg_tokens (access_token);
CREATE INDEX idx_ygg_tokens_user ON ygg_tokens (user_id);

CREATE TABLE ygg_join_sessions (
  id           BIGSERIAL PRIMARY KEY,
  access_token VARCHAR(64) NOT NULL,
  uuid         VARCHAR(32) NOT NULL,
  server_id    VARCHAR(64) NULL,
  server_ip    VARCHAR(45) NULL,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP,
  expires_at   TIMESTAMPTZ NOT NULL
);
CREATE INDEX idx_join_token ON ygg_join_sessions (access_token);

CREATE TABLE audit_log (
  id         BIGSERIAL PRIMARY KEY,
  actor_uid  BIGINT NULL,
  action     VARCHAR(64) NOT NULL,
  detail     TEXT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_audit_actor ON audit_log (actor_uid);
CREATE INDEX idx_audit_action ON audit_log (action);
