package store

import "context"

// Export reads omit the portal's defensive row caps so an attachment never
// silently truncates tenant data. Queries remain tenant/server scoped.
func (m *Mysql) ListSAServersForExport(ctx context.Context, tenantID int64) ([]SAServer, error) {
	return m.listSAServers(ctx, tenantID, "", 0)
}

func (m *Mysql) ListSAServerWhitelistForExport(ctx context.Context, tenantID, serverID int64) ([]SAServerWhitelist, error) {
	return m.listSAServerWhitelist(ctx, tenantID, serverID, 0)
}

func (m *Mysql) ListSAServerBansForExport(ctx context.Context, tenantID, serverID int64) ([]SAServerBan, error) {
	return m.listSAServerBans(ctx, tenantID, serverID, 0)
}
