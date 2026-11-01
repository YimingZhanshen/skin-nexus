-- Plugin platform v2 foundation (2026-10-28): installed metadata, encrypted
-- config secrets, namespaced KV/documents, and host-recorded plugin schema
-- versions. DDL-only by convention (see internal/migrate/tracked.go): every
-- data backfill lives in Go, because pre-tracking databases re-run unmarked
-- migration files.
--
-- Enable/disable authority stays in options (`plugin_enabled:<name>`) so the
-- v1 rollback path remains intact; plugin_meta mirrors lifecycle state for the
-- admin surface and future install/upgrade flows.
CREATE TABLE IF NOT EXISTS plugin_meta (
  name          VARCHAR(64)  NOT NULL,
  version       VARCHAR(64)  NOT NULL DEFAULT '',
  hook_api      INT          NOT NULL DEFAULT 1,
  runtime       VARCHAR(16)  NOT NULL DEFAULT 'goja',
  manifest      JSON         NULL,
  config        JSON         NULL,
  schema_version INT         NOT NULL DEFAULT 0,
  source        VARCHAR(16)  NOT NULL DEFAULT 'manual',
  checksum      CHAR(64)     NOT NULL DEFAULT '',
  state         VARCHAR(16)  NOT NULL DEFAULT 'discovered',
  last_error    TEXT         NULL,
  installed_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (name)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS plugin_secrets (
  plugin      VARCHAR(64)  NOT NULL,
  k           VARCHAR(100) NOT NULL,
  ciphertext  BLOB         NOT NULL,
  nonce       VARBINARY(12) NOT NULL,
  key_version INT          NOT NULL DEFAULT 1,
  updated_at  DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (plugin, k)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS plugin_kv (
  plugin     VARCHAR(64)  NOT NULL,
  ns         VARCHAR(64)  NOT NULL DEFAULT '',
  k          VARCHAR(191) NOT NULL,
  v          MEDIUMTEXT   NOT NULL,
  expires_at DATETIME(3)  NULL,
  updated_at DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (plugin, ns, k),
  KEY idx_pkv_expiry (plugin, expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS plugin_docs (
  plugin     VARCHAR(64)  NOT NULL,
  coll       VARCHAR(64)  NOT NULL,
  id         VARCHAR(191) NOT NULL,
  doc        JSON         NOT NULL,
  created_at DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (plugin, coll, id),
  KEY idx_pdocs_updated (plugin, coll, updated_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS plugin_migrations (
  plugin     VARCHAR(64) NOT NULL,
  version    INT         NOT NULL,
  applied_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (plugin, version)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
