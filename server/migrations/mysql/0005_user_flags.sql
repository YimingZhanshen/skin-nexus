-- Schema-alignment fix (found by the CI MySQL integration suite, 2026-10-23):
-- the code has always read/written users.email_verified and
-- users.can_join_server (scanUser/CreateUser — they mirror the legacy
-- yggdrasil-connect users columns), but 0001_init.sql predates that and
-- omitted them. Fresh databases created from the migration chain alone hit
-- "Unknown column 'email_verified'" on the first user insert.
--
-- Existing databases migrated from the legacy schema already carry both
-- columns; 0005 adds them only for schema-chain-created databases. On a
-- legacy-migrated DB where the columns exist, run this file only after
-- checking — the defaults below match the legacy semantics
-- (email_verified=0 pending, can_join_server=1 allowed).
-- 每条 ALTER 独立成句（review A2 2026-10-23）：合并写法在"两列已存在其一"
-- 的库上整句 ER1060 被容忍后标记已应用，另一列永远缺失。独立成句各自
-- 碰撞、各自收敛。
ALTER TABLE users
  ADD COLUMN email_verified TINYINT(1) NOT NULL DEFAULT 0 AFTER player_limit;
ALTER TABLE users
  ADD COLUMN can_join_server TINYINT(1) NOT NULL DEFAULT 1 AFTER email_verified;
