-- Durable plugin jobs: cursor/progress persist after each step so a worker can
-- resume after a restart. created_at/updated_at/run_after are written by the
-- scheduler rather than by a column default — the lease math compares them
-- against the values Go supplies.
CREATE TABLE host_jobs (
  id           VARCHAR(26) NOT NULL PRIMARY KEY,
  plugin       VARCHAR(64) NOT NULL,
  handler      VARCHAR(64) NOT NULL,
  owner_uid    INTEGER NOT NULL,
  state        VARCHAR(16) NOT NULL,
  payload      TEXT NOT NULL,
  job_cursor   TEXT NULL,
  result       TEXT NULL,
  progress     INTEGER NOT NULL DEFAULT 0,
  message      VARCHAR(255) NOT NULL DEFAULT '',
  last_error   TEXT NULL,
  attempts     INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 3,
  reclaims     INTEGER NOT NULL DEFAULT 0,
  run_after    DATETIME NOT NULL,
  lease_owner  VARCHAR(64) NOT NULL DEFAULT '',
  lease_until  DATETIME NULL,
  created_at   DATETIME NOT NULL,
  updated_at   DATETIME NOT NULL,
  started_at   DATETIME NULL,
  finished_at  DATETIME NULL
);
CREATE INDEX idx_host_jobs_claim ON host_jobs (state, run_after);
CREATE INDEX idx_host_jobs_plugin ON host_jobs (plugin, state);
CREATE INDEX idx_host_jobs_owner ON host_jobs (owner_uid);
