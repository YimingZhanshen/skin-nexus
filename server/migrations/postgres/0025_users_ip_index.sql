-- regs_per_ip counts accounts sharing a registrant address on every
-- registration (site_options.go CountUsersByIP, called from webapi before the
-- account is created). users is the one table every page touches, and its only
-- indexes were the two natural keys, so the per-registration cap cost a full
-- scan of every account the site has ever made.
CREATE INDEX idx_users_ip ON users (ip);
