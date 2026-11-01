-- 0009_schema_align.sql — users 表形状自愈（review A2 P1, 2026-10-23）
--
-- 问题：0001 的 CREATE TABLE users 与既有同名表碰撞时被 ER1050 容忍并
-- 标记"已应用"——一张由旧版 0001 建出的 users 缺少这些列时，登录会以
-- "Unknown column 'password_algo'" 全量 401 的形态出现（看起来像所有人
-- 密码都错）。此文件每条 ALTER 独立成句、重跑到 ER1060 被容忍，把
-- users 形状收敛到当前契约。
ALTER TABLE users ADD COLUMN password_algo VARCHAR(32) NOT NULL DEFAULT 'argon2id';
ALTER TABLE users ADD COLUMN player_limit INT NOT NULL DEFAULT 3;
ALTER TABLE users ADD COLUMN avatar_tid BIGINT UNSIGNED NULL;
ALTER TABLE users ADD COLUMN email_verified TINYINT(1) NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN can_join_server TINYINT(1) NOT NULL DEFAULT 1;
