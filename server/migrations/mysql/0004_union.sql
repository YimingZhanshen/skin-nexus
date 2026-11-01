-- MUA Union member-site persistence (phase 2, task H2).
-- Scope note: the Union core is deliberately stateless per request. Runtime
-- state that must survive restarts is small and is carried by the existing
-- options table (docs/phase2-dev-plan.md §6 "union 配置并入 options"):
--   union_member_key            shared member token (rotated by updatebackendkey)
--   union_server_list           cached GET /serverlist payload (JSON)
--   union_server_list_version   cached server list version
--   union_private_key_version   version of the hub-issued signing key
-- UUID remap targets live in uuid_map (0001_init.sql), audit rows in
-- audit_log (actor_uid NULL = system/union events).
--
-- union_kv is the escape hatch for future union-owned state (e.g. persisted
-- rotation bookkeeping or the OAuth2 signing pair if it is ever moved out of
-- options): generic k/v with an update timestamp, same conventions as 0002.
CREATE TABLE union_kv (
  k          VARCHAR(100) NOT NULL,
  v          TEXT NULL,
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (k)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
