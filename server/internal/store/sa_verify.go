package store

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"skin-nexus/server/internal/dbx"
)

const saJoinEventColumns = `e.id,e.tenant_id,e.server_id,e.join_session_id,e.uid,e.uuid,e.player_name,e.outcome,e.reason_code,e.source_mode,e.duration_ms,e.request_id,e.created_at`

func scanSAJoinEvent(s rowScanner) (SAJoinEvent, error) {
	var event SAJoinEvent
	err := s.Scan(&event.ID, &event.TenantID, &event.ServerID, &event.JoinSessionID, &event.UID,
		&event.UUID, &event.PlayerName, &event.Outcome, &event.ReasonCode, &event.SourceMode,
		&event.DurationMS, &event.RequestID, &event.CreatedAt)
	return event, err
}

// InsertSAJoinEvent is append-only and idempotent for a correlated local join.
// The INSERT SELECT binds the event to the credential-derived tenant/server;
// no caller-supplied tenant can write an event into another tenant's server.
func (m *Mysql) InsertSAJoinEvent(ctx context.Context, event SAJoinEvent) error {
	cols := []string{"tenant_id", "server_id", "join_session_id", "uid", "uuid", "player_name", "outcome", "reason_code", "source_mode", "duration_ms", "request_id", "created_at"}
	query := `INSERT INTO sa_join_events (` + strings.Join(cols, ",") + `)
		SELECT ?,?,?,?,?,?,?,?,?,?,?,? FROM sa_servers s JOIN sa_tenants t ON t.id=s.tenant_id
		WHERE s.id=? AND s.tenant_id=? AND t.status='active' AND t.deleted_at IS NULL`
	res, err := m.db.ExecContext(ctx, query, event.TenantID, event.ServerID,
		event.JoinSessionID, event.UID, event.UUID, event.PlayerName, event.Outcome, event.ReasonCode,
		event.SourceMode, event.DurationMS, event.RequestID, m.now().UTC(), event.ServerID, event.TenantID)
	if err != nil {
		if !dbx.IsDuplicateKey(err) {
			// Preserve real write failures (truncation, foreign keys, triggers);
			// only a correlated replay may become a successful no-op.
			return err
		}
		return m.saJoinEventScopeExists(ctx, event.TenantID, event.ServerID)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// An inactive/missing scope is an event-write failure and must reach the
		// verifier's alert hook rather than looking like a successful insert.
		return m.saJoinEventScopeExists(ctx, event.TenantID, event.ServerID)
	}
	return nil
}

func (m *Mysql) saJoinEventScopeExists(ctx context.Context, tenantID, serverID int64) error {
	var one int
	err := m.db.QueryRowContext(ctx,
		`SELECT 1 FROM sa_servers s JOIN sa_tenants t ON t.id=s.tenant_id
		 WHERE s.id=? AND s.tenant_id=? AND t.status='active' AND t.deleted_at IS NULL`,
		serverID, tenantID).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func (m *Mysql) ListSAJoinEvents(ctx context.Context, tenantID, serverID int64, from, to time.Time, limit int) ([]SAJoinEvent, error) {
	return m.ListSAJoinEventsPage(ctx, tenantID, serverID, from, to, 0, limit)
}

// ListSAJoinEventsPage reads a stable descending id cursor within a tenant/server
// scope. Callers request one extra row to derive hasMore without a count query.
func (m *Mysql) ListSAJoinEventsPage(ctx context.Context, tenantID, serverID int64, from, to time.Time, before int64, limit int) ([]SAJoinEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	query := `SELECT ` + saJoinEventColumns + ` FROM sa_join_events e JOIN sa_servers s ON s.id=e.server_id
		 WHERE s.tenant_id=? AND e.server_id=? AND e.created_at>=? AND e.created_at<=?`
	args := []any{tenantID, serverID, from.UTC(), to.UTC()}
	if before > 0 {
		query += ` AND e.id<?`
		args = append(args, before)
	}
	// The public cursor is an id, so ordering by timestamp as well could skip
	// rows when concurrent transactions commit out of timestamp order.
	query += ` ORDER BY e.id DESC LIMIT ?`
	rows, err := m.db.QueryContext(ctx, query, append(args, limit)...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]SAJoinEvent, 0)
	for rows.Next() {
		event, err := scanSAJoinEvent(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, event)
	}
	return out, rows.Err()
}

// LookupSAPlayer resolves only the identity needed by whitelist/ban forms. It
// deliberately avoids the global player/profile projection and its texture joins.
func (m *Mysql) LookupSAPlayer(ctx context.Context, query string) (SAPlayerIdentity, error) {
	query = strings.TrimSpace(query)
	where, value := m.db.Dialect().CIEquals("p.name"), query
	if len(query) == 32 {
		if _, err := hex.DecodeString(query); err == nil {
			where, value = "u.uuid = ?", strings.ToLower(query)
		}
	}
	var player SAPlayerIdentity
	err := m.db.QueryRowContext(ctx,
		`SELECT p.uid,p.name,COALESCE(u.uuid,'') FROM players p
		 LEFT JOIN uuid_map u ON u.player_id=p.id WHERE `+where+` LIMIT 1`, value).
		Scan(&player.UID, &player.Name, &player.UUID)
	if errors.Is(err, sql.ErrNoRows) {
		return SAPlayerIdentity{}, ErrNotFound
	}
	return player, err
}

// AdmitSAQuota atomically increments one server/window/counter and decides
// admission from that same increment. The caller supplies one UTC snapshot for
// both the window and expiry comparison; no second read or clock sample can
// attribute another request's increment to this one.
func (m *Mysql) AdmitSAQuota(ctx context.Context, serverID int64, windowStart time.Time, counter string, limit int, now time.Time) (int64, bool, error) {
	windowStart, now = windowStart.UTC(), now.UTC()
	expiresAt := windowStart.Add(time.Minute)
	query, returning := m.db.Dialect().AtomicCounterIncrement(
		"sa_quota_usage", []string{"server_id", "window_start", "counter"}, "value", "expires_at")
	args := []any{serverID, windowStart, saCounter(counter), expiresAt, now, expiresAt}
	var used int64
	if returning {
		if err := m.db.QueryRowContext(ctx, query, args...).Scan(&used); err != nil {
			return 0, false, err
		}
	} else {
		res, err := m.db.ExecContext(ctx, query, args...)
		if err != nil {
			return 0, false, err
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return 0, false, err
		}
		if affected == 1 {
			// A fresh insert starts at one and has no AUTO_INCREMENT id from
			// which LAST_INSERT_ID could report the counter value.
			used = 1
		} else {
			used, err = res.LastInsertId()
			if err != nil {
				return 0, false, err
			}
		}
	}
	if used <= 0 {
		return 0, false, errors.New("store: atomic quota increment returned no count")
	}
	return used, used <= int64(limit), nil
}

func (m *Mysql) ReadSAQuota(ctx context.Context, serverID int64, windowStart time.Time, counter string) (int64, error) {
	var value int64
	err := m.db.QueryRowContext(ctx,
		`SELECT value FROM sa_quota_usage WHERE server_id=? AND window_start=? AND counter=? AND expires_at>?`,
		serverID, windowStart.UTC(), saCounter(counter), m.now().UTC()).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	return value, err
}

func saCounter(counter string) string {
	if len(counter) > 64 {
		return counter[:64]
	}
	return counter
}
