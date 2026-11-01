-- MUA Union member-site persistence. Same scope notes as
-- migrations/mysql/0004_union.sql: hub config lives in options, and union_kv
-- is the escape hatch for union-owned state that must survive a restart.
CREATE TABLE union_kv (
  k          VARCHAR(100) NOT NULL PRIMARY KEY,
  v          TEXT NULL,
  updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP
);
