package store

// access_detail.go — the writer and the two sweepers behind the diagnostic
// window (tickets 5c / 5e, table access_log_details from migration 0029).
//
// Why this file exists instead of SQL in a handler: internal/store is the only
// home for engine-shaped SQL in this project (the AST gates under
// internal/dialect and internal/dbx police exactly that), and the detail row is
// written from inside the summary insert — its primary key IS the summary row's
// id, which nothing outside this package ever sees.
//
// Two clocks, deliberately apart:
//
//	captured_at        → PurgeAccessDetails, window = the panel option
//	                     access_detail_retention_days (default 90), read live.
//	credentials_until  → SweepAccessDetailCredentials, fixed 24h (accesslog.
//	                     CredentialTTL), NOT the retention option.
//
// The second one is the reason this file has a comment. 90 days is how long the
// site keeps a LOG LINE; it is not permission to keep a usable accessToken or
// web-session token for a quarter. A credential copied out of the table has no
// expiry date of its own and no undo — so the row outlives its credential by
// design: the sweep wipes credential_ref and resets credentials_until to NULL
// and leaves every other column (and the row itself) for the retention clock to
// decide about.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log"
	"time"

	"skin-nexus/server/internal/accesslog"
	"skin-nexus/server/internal/dbx"
)

// minDetailRetention is the floor both sweepers accept, mirroring
// PurgeAccessLogs: a mis-set knob may shorten a window, never zero it, because
// a zero window means "delete everything" on the next tick.
const minDetailRetention = 24 * time.Hour

// ErrAccessDetailDuplicate reports that access_log_details already holds a row
// for this access_logs.id. The 1:1 link is the primary key, so this is the
// intended answer to a double write — not a corruption to propagate.
var ErrAccessDetailDuplicate = errors.New("store: access detail row already exists")

// InsertAccessDetail writes the diagnostic row for the request whose summary
// row carries `id`. The capture is sanitized again here: Logger.Log applies the
// same gate, and this is the last place that stands between a header map and a
// durable column.
//
// captured_at comes from the store's Go clock (m.now), and credentials_until
// from the same instant + accesslog.CredentialTTL — one clock, so the pair is
// exactly 24h apart and comparable without a DB session timezone in the middle.
// The stamp is deliberately not CURRENT_TIMESTAMP: the sweep compares these
// values against a Go-side now(), and mixing the two clocks is the bug the
// whole diagnostic.go header exists to prevent.
func (m *Mysql) InsertAccessDetail(ctx context.Context, id int64, c *accesslog.Capture) error {
	c = c.Sanitized()
	if c == nil {
		return nil // nothing captured: no row, no error, nothing to sweep
	}
	captured := m.now().UTC()
	var until sql.NullTime
	if c.Credential != "" {
		// A credential and its expiry are written together or not at all.
		until = sql.NullTime{Time: captured.Add(accesslog.CredentialTTL), Valid: true}
	}
	_, err := m.db.ExecContext(ctx,
		`INSERT INTO access_log_details
			(id, status_code, duration_ms, upstream_code, headers_json, body_excerpt,
			 credential_ref, credentials_until, captured_at)
		 VALUES (?,?,?,?,?,?,?,?,?)`,
		id, c.StatusCode, c.DurationMS, c.UpstreamCode, c.HeadersJSON(), c.Body,
		c.Credential, until, captured)
	if err != nil {
		if dbx.IsDuplicateKey(err) {
			return fmt.Errorf("%w: access_logs.id %d", ErrAccessDetailDuplicate, id)
		}
		return fmt.Errorf("store: insert access detail %d: %w", id, err)
	}
	return nil
}

// PurgeAccessDetails drops detail rows older than the retention window
// (accesslog.Diagnostic.RetentionWindow). Returns the number deleted for the
// operator log line.
func (m *Mysql) PurgeAccessDetails(ctx context.Context, olderThan time.Duration) (int64, error) {
	if olderThan < minDetailRetention {
		olderThan = minDetailRetention
	}
	res, err := m.db.ExecContext(ctx,
		`DELETE FROM access_log_details WHERE captured_at < ?`,
		m.now().UTC().Add(-olderThan))
	if err != nil {
		return 0, fmt.Errorf("store: purge access_log_details: %w", err)
	}
	return res.RowsAffected()
}

// SweepAccessDetailCredentials wipes credential material whose fixed short
// deadline has passed, and clears the deadline so the row stops matching.
//
// It does NOT take a window parameter, and that is the point: the deadline was
// stamped at capture time as captured_at + accesslog.CredentialTTL (24h), so
// this query is the enforcement half of a promise the writer already made. If
// it followed the retention option instead, an operator who set 90 days would
// silently be keeping launchable access tokens for 90 days — retention governs
// log rows, never credentials.
//
// The row survives (only credential_ref and credentials_until change): the body
// excerpt and headers that made the window worth opening stay readable until
// PurgeAccessDetails says otherwise.
func (m *Mysql) SweepAccessDetailCredentials(ctx context.Context) (int64, error) {
	res, err := m.db.ExecContext(ctx,
		`UPDATE access_log_details
			SET credential_ref = '', credentials_until = NULL
		 WHERE credentials_until IS NOT NULL AND credentials_until < ?`,
		m.now().UTC())
	if err != nil {
		return 0, fmt.Errorf("store: sweep access detail credentials: %w", err)
	}
	return res.RowsAffected()
}

// logAccessDetailFailure reports a detail-write problem to the operator log
// without returning it. The summary row is already committed and the response
// is already on its way; a detail row is an extra, and the logger contract is
// that an extra never becomes a request failure. No retry either: a duplicate
// id would collide forever, and a wedged database must not be hammered by the
// request path.
func logAccessDetailFailure(id int64, err error) {
	if errors.Is(err, ErrAccessDetailDuplicate) {
		log.Printf("accesslog: detail row for access_logs.id=%d already present (1:1 primary key held): %v", id, err)
		return
	}
	log.Printf("accesslog: detail row for access_logs.id=%d not written: %v", id, err)
}
