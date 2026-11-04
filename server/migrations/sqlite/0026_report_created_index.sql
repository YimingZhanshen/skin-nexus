-- The panel's report queue opens with no status filter and the newest first
-- (reports.go ORDER BY r.created_at DESC). idx_report_status_time leads with
-- status, so it only helps once a filter is applied — the default view, which is
-- the one an operator actually opens, could not use it.
CREATE INDEX idx_report_created ON texture_reports (created_at);
