// union.go: store queries backing the MUA Union Hooks (inbound surface).
// Kept out of the union package per repo rule (new queries live in store).
package store

import (
	"context"
	"errors"
	"sort"
	"strings"

	"skin-nexus/server/internal/dbx"
)

// ErrUUIDConflict: the requested uuid is already mapped to another local row
// — a blind rewrite would corrupt identity (MUA review P1).
var ErrUUIDConflict = errors.New("uuid already mapped locally")

// isDupKey is this package's name for dbx's three-engine unique-violation
// test — the predicate behind every ErrDuplicate answer the API gives.
func isDupKey(err error) bool { return dbx.IsDuplicateKey(err) }

// ProfileNameByUUID returns {uuid: name} for every mapped player — backs
// union.Hooks.ProfileListFunc (POST /sync payload).
func (m *Mysql) ProfileNameByUUID(ctx context.Context) (map[string]string, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT u.uuid, p.name FROM uuid_map u JOIN players p ON p.id = u.player_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var uuid, name string
		if err := rows.Scan(&uuid, &name); err != nil {
			return nil, err
		}
		out[uuid] = name
	}
	return out, rows.Err()
}

// RemapUUIDs applies a hub-issued {old: new} batch to uuid_map. Only mapped
// rows (player_id NOT NULL) are touched. Hardened per MUA review P1
// (2026-10-23): deterministic sorted iteration (map order made chains
// nondeterministic), values normalized + validated to canonical 32-lowercase-
// hex before write (hub strings like dashed/uppercase uuids used to either
// abort the batch in strict mode or SILENTLY TRUNCATE into a wrong identity),
// and per-entry duplicate collisions skip instead of rolling back the whole
// batch (merge batches {"m1":"T","m2":"T"} and chains {"A":"B","B":"C"} are
// normal hub broadcasts). Returns the true applied/skipped counts so the
// audit trail reflects reality instead of len(batch).
func (m *Mysql) RemapUUIDs(ctx context.Context, remap map[string]string) (applied, skipped int, err error) {
	keys := make([]string, 0, len(remap))
	for k := range remap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	tx, err := m.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, 0, err
	}
	defer tx.Rollback()
	for _, rawOld := range keys {
		old := normalizeUUID(rawOld)
		new := normalizeUUID(remap[rawOld])
		if !isHexUUID(old) || !isHexUUID(new) || old == new {
			skipped++
			continue
		}
		// PostgreSQL aborts the entire transaction as soon as one statement
		// fails, so a collision cannot be caught and stepped over the way
		// MySQL tolerates it. Ask first, then write: the answer is the same
		// (never blind-overwrite an existing target uuid), and the batch keeps
		// its "one bad pair does not roll back the merge" property everywhere.
		var taken int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM uuid_map WHERE uuid = ?`, new).Scan(&taken); err != nil {
			return applied, skipped, err
		}
		if taken > 0 {
			skipped++ // collision: target row exists
			continue
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE uuid_map SET uuid = ? WHERE uuid = ? AND player_id IS NOT NULL`, new, old)
		if err != nil {
			if isDupKey(err) {
				skipped++ // race with a concurrent write — same verdict
				continue
			}
			return applied, skipped, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			applied++
		} else {
			skipped++ // unknown old — nothing local to rewrite
		}
	}
	return applied, skipped, tx.Commit()
}

// ConvergeUUID converges one local row after the hub accepted a user-initiated
// remap (MUA review P1: the requesting site used to answer ok while its OWN
// row kept the stale uuid — the exact 1.19+ signed-profile breakage the
// remap exists to fix). Returns ErrUUIDConflict when the target uuid is
// already mapped locally (merge needs a human), ErrNotFound when the me row
// is gone.
func (m *Mysql) ConvergeUUID(ctx context.Context, me, target string) error {
	res, err := m.db.ExecContext(ctx,
		`UPDATE uuid_map SET uuid = ? WHERE uuid = ? AND player_id IS NOT NULL`, target, me)
	if err != nil {
		if isDupKey(err) {
			return ErrUUIDConflict
		}
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// normalizeUUID canonicalizes to the uuid_map contract: 32 lowercase hex,
// no dashes (hub values arrive as untyped strings).
func normalizeUUID(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.ReplaceAll(s, "-", "")
}

func isHexUUID(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// PlayerUUIDsOwnedBy returns the canonical uuids of one user's players —
// ownership check helper for union user endpoints (defense in depth beside
// the per-player lookup).
func (m *Mysql) PlayerUUIDsOwnedBy(ctx context.Context, uid int64) (map[string]bool, error) {
	rows, err := m.db.QueryContext(ctx,
		`SELECT u.uuid FROM uuid_map u JOIN players p ON p.id = u.player_id WHERE p.uid = ?`, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var uuid string
		if err := rows.Scan(&uuid); err != nil {
			return nil, err
		}
		out[uuid] = true
	}
	return out, rows.Err()
}
