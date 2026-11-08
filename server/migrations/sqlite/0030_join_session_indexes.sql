-- Two statements, no index behind either one. FindLiveJoins (ygg hasJoined)
-- filters on uuid while the only index here covers access_token, so every
-- hasJoined scanned the table; and nothing ever deleted these rows — the 120s
-- TTL only hid them, so the scan cost grew with traffic. idx_join_uuid serves
-- the lookup (uuid equality, expires_at range), idx_join_expiry serves the
-- sweep added with it (store.PurgeJoinSessions, DELETE WHERE expires_at < ?).
CREATE INDEX idx_join_uuid ON ygg_join_sessions (uuid, expires_at);
CREATE INDEX idx_join_expiry ON ygg_join_sessions (expires_at);
