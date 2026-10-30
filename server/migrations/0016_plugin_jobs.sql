-- Durable plugin jobs: one row per submitted job; cursor/progress are
-- persisted after each step so workers can resume after restart.
CREATE TABLE host_jobs (
  id            VARCHAR(26) PRIMARY KEY,
  plugin        VARCHAR(64) NOT NULL,
  handler       VARCHAR(64) NOT NULL,
  owner_uid     BIGINT NOT NULL,
  state         VARCHAR(16) NOT NULL,
  payload       JSON NOT NULL,
  job_cursor    JSON NULL,
  result        JSON NULL,
  progress      TINYINT NOT NULL DEFAULT 0,
  message       VARCHAR(255) NOT NULL DEFAULT '',
  last_error    TEXT NULL,
  attempts      INT NOT NULL DEFAULT 0,
  max_attempts  INT NOT NULL DEFAULT 3,
  reclaims      INT NOT NULL DEFAULT 0,
  run_after     DATETIME(3) NOT NULL,
  lease_owner   VARCHAR(64) NOT NULL DEFAULT '',
  lease_until   DATETIME(3) NULL,
  created_at    DATETIME(3) NOT NULL,
  updated_at    DATETIME(3) NOT NULL,
  started_at    DATETIME(3) NULL,
  finished_at   DATETIME(3) NULL,
  KEY idx_host_jobs_claim (state, run_after),
  KEY idx_host_jobs_plugin (plugin, state),
  KEY idx_host_jobs_owner (owner_uid)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
