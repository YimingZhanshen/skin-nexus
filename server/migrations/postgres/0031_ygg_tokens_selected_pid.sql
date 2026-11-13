-- player_admin renames/owner-changes and AdminDeletePlayer all UPDATE
-- ygg_tokens WHERE selected_pid = ?, but selected_pid had no index on any
-- engine: every rename scanned a table that grows with users x tokens (W0
-- store scan A3). Plain CREATE INDEX is valid identically on all three.
CREATE INDEX idx_ygg_tokens_selected_pid ON ygg_tokens (selected_pid);
