// Package accesslog is the observability sidecar: web/launcher logins,
// Yggdrasil server traffic and Union traffic, surfaced in the admin panel.
//
// Contract: recording NEVER affects the request path — the zero value and
// a nil *Logger record nothing, a sink error is reported to the operator log
// and dropped (never retried, never turned into a response failure), and
// fields are truncated rune-safe to the access_logs column limits before
// they reach the store. The frozen /api/yggdrasil wire semantics cannot change
// because a log line failed.
package accesslog

import (
	"context"
	"log"
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
	// Capture carries the diagnostic-mode detail row for this same request
	// (nil = nothing to capture). Logger.Log drops it whenever the detail
	// window is closed, so the summary columns above are the ONLY thing a
	// closed-window request persists — the rules internal/ygg/
	// accesslog_credentials_test.go pins are unaffected by this field.
	Capture *Capture
}

// Sink persists one entry (main wires store.InsertAccessLog).
type Sink func(ctx context.Context, e Entry) error

// Logger records entries through an optional sink. Nil-safe: both the
// receiver and the sink may be absent.
type Logger struct {
	sink Sink
	// diag is the diagnostic-mode switch (nil = window permanently closed).
	diag *Diagnostic
}

// New builds a logger over a sink (nil sink = disabled logger).
func New(sink Sink) *Logger { return &Logger{sink: sink} }

// SetDiagnostic installs the detail-mode switch (boot wiring). Without it no
// detail row is ever written, whatever a recorder attached to an entry.
func (l *Logger) SetDiagnostic(d *Diagnostic) {
	if l == nil {
		return
	}
	l.diag = d
}

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
	// The window decides here, before the sink sees anything: closed means the
	// capture (headers, body excerpt, credential) is dropped, not stored empty.
	// Sanitized() is the whitelist + cap gate, so a recorder that handed over
	// the whole header map cannot smuggle a Cookie value past it.
	if e.Capture != nil {
		if l.diag.DiagnosticActive(ctx) {
			e.Capture = e.Capture.Sanitized()
		} else {
			e.Capture = nil
		}
	}
	if err := l.sink(context.WithoutCancel(ctx), e); err != nil {
		// Best effort, never a retry: the response is already on its way and a
		// logging failure must not turn into a request failure.
		log.Printf("accesslog: record %s/%s: %v", e.Kind, e.Outcome, err)
	}
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
