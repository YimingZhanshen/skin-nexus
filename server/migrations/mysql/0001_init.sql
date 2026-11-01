-- SkinNexus initial schema (MySQL 5.7+ / 8.0).
-- Field mapping from blessing-skin-server: analysis/blessing-skin-server.md §2.
-- Conventions: UTC DATETIME(3); utf8mb4; InnoDB.

CREATE TABLE users (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  email         VARCHAR(100) NULL,
  nickname      VARCHAR(50)  NOT NULL,
  password      VARCHAR(255) NOT NULL,          -- hash verbatim until lazy rehash
  password_algo VARCHAR(32)  NOT NULL DEFAULT 'argon2id', -- legacy rows: 'legacy:md5' etc (phase-1 addition, weak-hash not self-describing)
  player_limit  INT NOT NULL DEFAULT 3,
  ip            VARCHAR(45) NULL,
  avatar_tid    BIGINT UNSIGNED NULL,           -- legacy users.avatar = textures.tid
  created_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_users_email (email),            -- deduped + case-fold rules applied at migration
  UNIQUE KEY uq_users_nickname (nickname)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE players (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  uid         BIGINT UNSIGNED NOT NULL,
  name        VARCHAR(16)  NOT NULL,            -- MC name, case preserved
  tid_skin    BIGINT UNSIGNED NULL,             -- legacy -1/0 normalized to NULL at migration
  tid_cape    BIGINT UNSIGNED NULL,
  preference  VARCHAR(10) NULL,                 -- legacy leftover column, kept for mapping
  created_at  DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at  DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uq_players_name (name),
  KEY idx_players_uid (uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE textures (
  id          BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  uid         BIGINT UNSIGNED NOT NULL,
  type        ENUM('skin','cape') NOT NULL,     -- legacy steve/alex/cape mapped: steve->skin(classic), alex->skin(slim model via player preference), cape->cape
  hash        CHAR(64) NOT NULL,                -- sha256 hex of PNG bytes; file = <textures_dir>/<hash>
  model       ENUM('default','slim') NULL,      -- skin only; legacy type alex => slim
  description VARCHAR(255) NULL,
  upload_at   DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),  -- legacy bare DATETIME convention
  PRIMARY KEY (id),
  KEY idx_textures_uid_type (uid, type),
  KEY idx_textures_hash (hash)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- UUID assignments from yggdrasil-connect `uuid` table (zero-drift guarantee).
-- Rule: hex2bin(md5("OfflinePlayer:"+name)) v3 variant; renames do NOT recompute.
CREATE TABLE uuid_map (
  name      VARCHAR(16)  NOT NULL,
  uuid      CHAR(32)     NOT NULL,              -- 32 lowercase hex, no dashes
  player_id BIGINT UNSIGNED NULL,
  PRIMARY KEY (name),
  UNIQUE KEY uq_uuid_map_uuid (uuid),
  KEY idx_uuid_map_player (player_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE options (
  k VARCHAR(100) NOT NULL,
  v TEXT NULL,
  PRIMARY KEY (k)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- Yggdrasil sessions (access tokens are opaque; format differs from legacy
-- Passport JWT by design — all tokens die at cutover).
CREATE TABLE ygg_tokens (
  id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id      BIGINT UNSIGNED NOT NULL,
  client_token CHAR(32) NOT NULL,
  access_token CHAR(64) NOT NULL,
  selected_pid BIGINT UNSIGNED NULL,
  created_at   DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  expires_at   DATETIME(3) NOT NULL,
  invalidated  TINYINT(1) NOT NULL DEFAULT 0,
  PRIMARY KEY (id),
  UNIQUE KEY uq_ygg_tokens_access (access_token),
  KEY idx_ygg_tokens_user (user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- join sessions: TTL 120s (legacy behavior, blueprint §1.7).
CREATE TABLE ygg_join_sessions (
  id           BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  access_token CHAR(64) NOT NULL,
  uuid         CHAR(32) NOT NULL,
  server_id    VARCHAR(64) NULL,
  server_ip    VARCHAR(45) NULL,
  created_at   DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  expires_at   DATETIME(3) NOT NULL,
  PRIMARY KEY (id),
  KEY idx_join_token (access_token)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE audit_log (
  id         BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  actor_uid  BIGINT UNSIGNED NULL,
  action     VARCHAR(64) NOT NULL,
  detail     JSON NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  KEY idx_audit_actor (actor_uid),
  KEY idx_audit_action (action)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
