// Package accesslog is the observability sidecar: web/launcher logins,
// Yggdrasil server traffic and Union traffic, surfaced in the admin panel.
//
// Contract: recording NEVER affects the request path — the zero value and
// a nil *Logger record nothing, sink errors are swallowed, and fields are
// truncated rune-safe to the access_logs column limits before they reach
// the store. The frozen /api/yggdrasil wire semantics cannot change because
// a log line failed.
package accesslog

import (
	"context"
	"unicode/utf8"
)

// Kinds (column: kind VARCHAR(24)).
const (
	WebLogin        = "web_login"
	WebRegister     = "web_register"
	YggAuthenticate = "ygg_authenticate"
	YggRefresh      = "ygg_refresh"
	YggValidate     = "ygg_validate"
	YggJoin         = "ygg_join"
	YggHasJoined    = "ygg_hasjoined"
	YggProfile      = "ygg_profile"
	UnionIn         = "union_in"
	UnionOut        = "union_out"
)

// Outcomes (column: outcome VARCHAR(16)).
const (
	OutcomeOK     = "ok"     // request succeeded
	OutcomeFail   = "fail"   // rejected on business rules (bad password, no session)
	OutcomeDenied = "denied" // rejected by a guard (rate limit, lockout, disabled module)
	OutcomeMiss   = "miss"   // nothing to answer with (unknown profile/session)
	OutcomeError  = "error"  // internal failure (DB, hook panic path)
)

// Entry is one log line. UID 0 = no account behind the event.
type Entry struct {
	Kind    string
	Outcome string
	UID     int64
	Who     string // nickname / username / uuid / target path
	IP      string
	UA      string
	Detail  string // reason, serverId, status, latency…
}

// Sink persists one entry (main wires store.InsertAccessLog).
type Sink func(ctx context.Context, e Entry) error

// Logger records entries through an optional sink. Nil-safe: both the
// receiver and the sink may be absent.
type Logger struct {
	sink Sink
}

// New builds a logger over a sink (nil sink = disabled logger).
func New(sink Sink) *Logger { return &Logger{sink: sink} }

// Log records best-effort. The context is detached (WithoutCancel) so a
// client that disconnects mid-request still leaves its audit trace — the
// log is for operators, not for the caller.
func (l *Logger) Log(ctx context.Context, e Entry) {
	if l == nil || l.sink == nil {
		return
	}
	if e.Kind == "" || e.Outcome == "" {
		return
	}
	e.Who = cut(e.Who, 64)
	e.IP = cut(e.IP, 45)
	e.UA = cut(e.UA, 255)
	e.Detail = cut(e.Detail, 255)
	_ = l.sink(context.WithoutCancel(ctx), e)
}

// cut truncates to at most n BYTES without splitting a UTF-8 rune (MySQL
// utf8mb4 strict mode rejects broken sequences): slice, then peel bytes
// until the remainder is valid UTF-8 (or empty).
func cut(s string, n int) string {
	if len(s) <= n {
		return s
	}
	s = s[:n]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
