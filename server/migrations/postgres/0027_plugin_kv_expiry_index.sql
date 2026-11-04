-- The sweep deletes expired keys across every plugin (expires_at <= ? LIMIT ?),
-- while idx_pkv_expiry leads with plugin and is only usable by a per-plugin
-- delete. An unindexed sweep runs on each scheduled pass, and plugin_kv is the
-- table a stateful plugin grows without bound.
CREATE INDEX idx_pkv_expiry_at ON plugin_kv (expires_at);
