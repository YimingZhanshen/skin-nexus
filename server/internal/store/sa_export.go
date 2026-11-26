package store

import (
	"context"
	"errors"
	"fmt"
)

const (
	DefaultSAExportMaxServers     = 1000
	DefaultSAExportMaxWhitelist   = 10000
	DefaultSAExportMaxBans        = 10000
	DefaultSAExportMaxEvents      = 100000
	DefaultSAExportMaxCredentials = 10000
	DefaultSAExportMaxBytes       = 16 << 20
)

var ErrSAExportTooLarge = errors.New("store: server access export exceeds explicit limit")

type SAExportLimitError struct {
	Collection string
	Limit      int64
}

func (e *SAExportLimitError) Error() string {
	return fmt.Sprintf("%s exceeds export limit of %d", e.Collection, e.Limit)
}

func (e *SAExportLimitError) Unwrap() error { return ErrSAExportTooLarge }

type SAExportLimits struct {
	MaxServers     int
	MaxWhitelist   int
	MaxBans        int
	MaxEvents      int
	MaxCredentials int
	MaxBytes       int
}

func DefaultSAExportLimits() SAExportLimits {
	return SAExportLimits{
		MaxServers: DefaultSAExportMaxServers, MaxWhitelist: DefaultSAExportMaxWhitelist,
		MaxBans: DefaultSAExportMaxBans, MaxEvents: DefaultSAExportMaxEvents,
		MaxCredentials: DefaultSAExportMaxCredentials, MaxBytes: DefaultSAExportMaxBytes,
	}
}

// ResolveSAExportLimits applies the first-version hard caps. Callers may lower
// a limit for tests or deployments, but cannot raise it above the documented cap.
func ResolveSAExportLimits(limits SAExportLimits) SAExportLimits {
	defaults := DefaultSAExportLimits()
	limits.MaxServers = saExportLimitValue(limits.MaxServers, defaults.MaxServers)
	limits.MaxWhitelist = saExportLimitValue(limits.MaxWhitelist, defaults.MaxWhitelist)
	limits.MaxBans = saExportLimitValue(limits.MaxBans, defaults.MaxBans)
	limits.MaxEvents = saExportLimitValue(limits.MaxEvents, defaults.MaxEvents)
	limits.MaxCredentials = saExportLimitValue(limits.MaxCredentials, defaults.MaxCredentials)
	limits.MaxBytes = saExportLimitValue(limits.MaxBytes, defaults.MaxBytes)
	return limits
}

func saExportLimitValue(value, hardLimit int) int {
	if value <= 0 || value > hardLimit {
		return hardLimit
	}
	return value
}

type SAExportData struct {
	Servers     []SAServer
	Policies    map[int64]SAServerPolicy
	Whitelist   map[int64][]SAServerWhitelist
	Bans        map[int64][]SAServerBan
	Events      map[int64][]SAOwnerJoinEvent
	Credentials map[int64][]SACredential
}

// SAExportData reads a tenant with six queries regardless of server count. Each
// query asks for one row beyond its cap so overflow is an explicit error, never
// a silently truncated attachment. Credential queries select metadata only.
func (m *Mysql) SAExportData(ctx context.Context, tenantID int64, limits SAExportLimits) (SAExportData, error) {
	limits = ResolveSAExportLimits(limits)
	servers, err := m.listSAServers(ctx, tenantID, "", limits.MaxServers+1)
	if err != nil {
		return SAExportData{}, err
	}
	if len(servers) > limits.MaxServers {
		return SAExportData{}, saExportLimit("servers", limits.MaxServers)
	}
	out := SAExportData{
		Servers: servers, Policies: make(map[int64]SAServerPolicy, len(servers)),
		Whitelist: make(map[int64][]SAServerWhitelist, len(servers)), Bans: make(map[int64][]SAServerBan, len(servers)),
		Events: make(map[int64][]SAOwnerJoinEvent, len(servers)), Credentials: make(map[int64][]SACredential, len(servers)),
	}
	if len(servers) == 0 {
		return out, nil
	}
	serverIDs := make([]int64, len(servers))
	for i := range servers {
		serverIDs[i] = servers[i].ID
	}
	placeholders, args := saIDPlaceholders(serverIDs)
	rows, err := m.db.QueryContext(ctx,
		saPolicySelect+` WHERE s.tenant_id=? AND p.server_id IN (`+placeholders+`)`, append([]any{tenantID}, args...)...)
	if err != nil {
		return SAExportData{}, err
	}
	for rows.Next() {
		policy, err := scanSAPolicy(rows)
		if err != nil {
			_ = rows.Close()
			return SAExportData{}, err
		}
		out.Policies[policy.ServerID] = policy
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return SAExportData{}, err
	}
	if err := rows.Close(); err != nil {
		return SAExportData{}, err
	}
	if err := m.saExportWhitelist(ctx, tenantID, serverIDs, limits.MaxWhitelist, out.Whitelist); err != nil {
		return SAExportData{}, err
	}
	if err := m.saExportBans(ctx, tenantID, serverIDs, limits.MaxBans, out.Bans); err != nil {
		return SAExportData{}, err
	}
	if err := m.saExportEvents(ctx, tenantID, serverIDs, limits.MaxEvents, out.Events); err != nil {
		return SAExportData{}, err
	}
	if err := m.saExportCredentials(ctx, serverIDs, limits.MaxCredentials, out.Credentials); err != nil {
		return SAExportData{}, err
	}
	return out, nil
}

func (m *Mysql) saExportWhitelist(ctx context.Context, tenantID int64, serverIDs []int64, limit int, out map[int64][]SAServerWhitelist) error {
	placeholders, args := saIDPlaceholders(serverIDs)
	rows, err := m.db.QueryContext(ctx,
		`SELECT w.server_id,w.uid,w.note,w.created_at FROM sa_server_whitelist w
		 JOIN sa_servers s ON s.id=w.server_id WHERE s.tenant_id=? AND w.server_id IN (`+placeholders+`)
		 ORDER BY w.server_id,w.id DESC LIMIT ?`, append(append([]any{tenantID}, args...), limit+1)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var row SAServerWhitelist
		if err := rows.Scan(&row.ServerID, &row.UID, &row.Note, &row.CreatedAt); err != nil {
			return err
		}
		count++
		if count > limit {
			return saExportLimit("whitelist", limit)
		}
		out[row.ServerID] = append(out[row.ServerID], row)
	}
	return rows.Err()
}

func (m *Mysql) saExportBans(ctx context.Context, tenantID int64, serverIDs []int64, limit int, out map[int64][]SAServerBan) error {
	placeholders, args := saIDPlaceholders(serverIDs)
	rows, err := m.db.QueryContext(ctx,
		`SELECT b.server_id,b.uid,b.reason,b.expires_at,b.created_at FROM sa_server_bans b
		 JOIN sa_servers s ON s.id=b.server_id WHERE s.tenant_id=? AND b.server_id IN (`+placeholders+`)
		 ORDER BY b.server_id,b.id DESC LIMIT ?`, append(append([]any{tenantID}, args...), limit+1)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var row SAServerBan
		if err := rows.Scan(&row.ServerID, &row.UID, &row.Reason, &row.ExpiresAt, &row.CreatedAt); err != nil {
			return err
		}
		count++
		if count > limit {
			return saExportLimit("bans", limit)
		}
		out[row.ServerID] = append(out[row.ServerID], row)
	}
	return rows.Err()
}

func (m *Mysql) saExportEvents(ctx context.Context, tenantID int64, serverIDs []int64, limit int, out map[int64][]SAOwnerJoinEvent) error {
	placeholders, args := saIDPlaceholders(serverIDs)
	rows, err := m.db.QueryContext(ctx,
		`SELECT e.server_id,e.id,e.created_at,e.outcome,e.reason_code,e.source_mode,e.duration_ms
		 FROM sa_join_events e JOIN sa_servers s ON s.id=e.server_id
		 WHERE s.tenant_id=? AND e.server_id IN (`+placeholders+`)
		 ORDER BY e.server_id,e.id DESC LIMIT ?`, append(append([]any{tenantID}, args...), limit+1)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var serverID int64
		var row SAOwnerJoinEvent
		if err := rows.Scan(&serverID, &row.ID, &row.CreatedAt, &row.Outcome, &row.ReasonCode, &row.SourceMode, &row.DurationMS); err != nil {
			return err
		}
		count++
		if count > limit {
			return saExportLimit("events", limit)
		}
		out[serverID] = append(out[serverID], row)
	}
	return rows.Err()
}

func (m *Mysql) saExportCredentials(ctx context.Context, serverIDs []int64, limit int, out map[int64][]SACredential) error {
	placeholders, args := saIDPlaceholders(serverIDs)
	rows, err := m.db.QueryContext(ctx,
		`SELECT c.server_id,c.id,c.mode,c.status,c.created_at,c.last_used_at,c.expires_at
		 FROM sa_server_credentials c WHERE c.server_id IN (`+placeholders+`)
		 ORDER BY c.server_id,c.id DESC LIMIT ?`, append(args, limit+1)...)
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var row SACredential
		if err := rows.Scan(&row.ServerID, &row.ID, &row.Mode, &row.Status, &row.CreatedAt, &row.LastUsedAt, &row.ExpiresAt); err != nil {
			return err
		}
		count++
		if count > limit {
			return saExportLimit("credentials", limit)
		}
		out[row.ServerID] = append(out[row.ServerID], row)
	}
	return rows.Err()
}

func saExportLimit(collection string, limit int) error {
	return &SAExportLimitError{Collection: collection, Limit: int64(limit)}
}
