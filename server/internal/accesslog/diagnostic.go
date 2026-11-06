// Diagnostic mode switch (2026-11-05 拍板).
//
// The access logger normally writes one lightweight summary row per request.
// "Diagnostic mode" turns on an extra per-request DETAIL writer (a later slice
// owns that writer, the reader API and the sweeper — this file is only the
// switch and its retention setting). The switch is the option
// `access_detail_until`, whose value is a UTC RFC3339 timestamp: when that
// timestamp is still in the future, the mode is ON and the detail writer fires.
//
// The expiry is decided with the Go clock, never SQL now(): now() follows the
// DB session time zone, so a row that is "expired" to MySQL may still read as
// live here (and vice versa) on a server set to UTC. The jobs subsystem hit the
// same hazard and settled on the same rule — compare against time.Now().UTC().
// The clock is an injectable field so a test can advance it past `until` and
// watch the predicate flip WITHOUT any SQL being re-evaluated.
package accesslog

import (
	"context"
	"strconv"
	"time"
)

// Option keys for the diagnostic-mode feature.
const (
	// DetailUntilOption holds the UTC RFC3339 expiry of the detail window; a
	// future value = diagnostic mode ON. Written only by the admin API.
	DetailUntilOption = "access_detail_until"
	// DetailRetentionOption is the panel-editable retention (days) the detail
	// sweeper trims captured_at against. It is a normal settings-whitelist key
	// (see internal/adminapi/settings.go), read here only for the default.
	DetailRetentionOption = "access_detail_retention_days"
	// DefaultDetailRetentionDays is the owner-decided default (90) used when
	// the option is absent or unreadable.
	DefaultDetailRetentionDays = 90
	// MaxDetailRetentionDays clamps the option for the sweeper's duration math.
	// Without it, an option typed as 999999999 days overflows int64 nanoseconds
	// into a NEGATIVE window, and a negative window makes the cutoff land in the
	// future — i.e. "delete every detail row on the next tick". Ten years of
	// access logs is far past any reason to keep them, and far inside the range.
	MaxDetailRetentionDays = 3650
)

// OptionReader reads one site option (ok = the row exists). It is a func type
// so the predicate stays testable without a database and so adminapi's store
// can be injected here.
type OptionReader func(ctx context.Context, key string) (string, bool, error)

// Diagnostic is the detail-mode switch reader. The zero value and a nil
// receiver report "off", so the later writer slices can call it from a nil-safe
// logger path without a guard. Now is the injectable clock (nil = time.Now().UTC).
type Diagnostic struct {
	Option OptionReader
	Now    func() time.Time
}

// now returns the injected clock or the wall clock in UTC — never SQL now().
func (d *Diagnostic) now() time.Time {
	if d == nil || d.Now == nil {
		return time.Now().UTC()
	}
	return d.Now().UTC()
}

// DiagnosticActive reports whether the detail writer should fire for a request:
// the access_detail_until option parses as an RFC3339 time strictly in the
// future according to the Go clock. Absent, empty, unparseable, or already
// past — all read as OFF (fail closed: no detail rows unless a super admin
// deliberately opened a live window).
func (d *Diagnostic) DiagnosticActive(ctx context.Context) bool {
	if d == nil || d.Option == nil {
		return false
	}
	raw, ok, err := d.Option(ctx, DetailUntilOption)
	if !ok || err != nil || raw == "" {
		return false
	}
	until, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return false
	}
	return d.now().Before(until.UTC())
}

// DiagnosticUntil returns the parsed expiry (ok = a live future window) plus
// the whole seconds remaining on the injected clock. Used by the GET endpoint
// so the panel shows the same decision the writer makes.
func (d *Diagnostic) DiagnosticUntil(ctx context.Context) (time.Time, int64, bool) {
	if d == nil || d.Option == nil {
		return time.Time{}, 0, false
	}
	raw, ok, err := d.Option(ctx, DetailUntilOption)
	if !ok || err != nil || raw == "" {
		return time.Time{}, 0, false
	}
	until, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, 0, false
	}
	until = until.UTC()
	now := d.now()
	if !now.Before(until) {
		return time.Time{}, 0, false
	}
	return until, int64(until.Sub(now).Seconds()), true
}

// RetentionDays returns the configured detail retention in days (the sweeper's
// window), falling back to DefaultDetailRetentionDays when the option is
// absent, blank, non-numeric or non-positive.
func (d *Diagnostic) RetentionDays(ctx context.Context) int {
	if d == nil || d.Option == nil {
		return DefaultDetailRetentionDays
	}
	raw, ok, err := d.Option(ctx, DetailRetentionOption)
	if !ok || err != nil {
		return DefaultDetailRetentionDays
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return DefaultDetailRetentionDays
	}
	return n
}

// RetentionWindow converts RetentionDays into the duration the detail sweeper
// trims `captured_at` against. It reads the option on every call: the panel
// value is hot (an operator who shortens 90d to 7d expects the next tick to
// obey), and the clamp keeps an absurd value from overflowing into a negative
// window that would delete the table.
//
// This is the LOG-retention clock. Credential material does not run on it — see
// CredentialTTL and store.SweepAccessDetailCredentials.
func (d *Diagnostic) RetentionWindow(ctx context.Context) time.Duration {
	days := d.RetentionDays(ctx)
	if days > MaxDetailRetentionDays {
		days = MaxDetailRetentionDays
	}
	return time.Duration(days) * 24 * time.Hour
}
