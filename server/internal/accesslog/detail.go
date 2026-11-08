// Detail capture (ticket 5c, 2026-11-05 拍板).
//
// The summary logger writes one 255-byte line per request; when a super admin
// opens the diagnostic window (`access_detail_until`, see diagnostic.go) the
// same request also gets a row in access_log_details — status, latency, the two
// headers that identify a launcher, a bounded body excerpt and, for the length
// of that window only, the credential the request carried.
//
// Two rules this file owns:
//
//  1. The window is the only thing that lets capture material out of the
//     logger. Logger.Log drops the whole capture when DiagnosticActive is false,
//     so a closed window leaves nothing for a sink to persist — not an empty
//     credential_ref, no detail row at all. A nil Diagnostic (the default for
//     every logger built by accesslog.New) reads as closed: the feature fails
//     closed, and a boot path that forgets to wire it captures nothing.
//  2. Headers are whitelisted, never blacklisted. Cookie, Authorization and
//     X-Union-Member-Key all carry secrets on this surface, and a growing
//     deny-list misses the next one; two named headers is a rule that can be
//     read and audited. The credential has exactly one home (credential_ref)
//     and headers_json never duplicates it.
package accesslog

import (
	"context"
	"encoding/json"
	"net/http"
	"time"
)

const (
	// MaxBodyExcerpt bounds body_excerpt. 4 KiB is four times the 1 KiB that a
	// Yggdrasil/union JSON answer actually occupies (a full authenticate
	// response with two profiles is ~600 B, an error body ~120 B), so a
	// debugging operator reads the whole payload, while a runaway HTML error
	// page from a reverse proxy — the case that produces the fat bodies —
	// cannot bloat the row: MySQL TEXT caps at 64 KiB and every detail row is
	// read back inside a panel page that pages at 50 rows.
	MaxBodyExcerpt = 4096
	// MaxUpstreamCode matches access_log_details.upstream_code VARCHAR(64);
	// clipping here beats a strict-mode insert error that loses the whole row.
	MaxUpstreamCode = 64
	// MaxCredentialRef bounds credential_ref. Real values are 36–72 bytes
	// (ygg accessToken `uuid-uuid`, web session token 64 hex); the cap exists
	// only so a hostile caller cannot park an arbitrarily large secret in the
	// database through a header or a body field.
	MaxCredentialRef = 512
)

// CredentialTTL is the fixed short clock that decides when a captured
// credential stops being readable. It is NOT the retention option: 90 days is
// how long the site keeps a log line, not permission to keep a live token for a
// quarter. The store stamps credentials_until = captured_at + CredentialTTL and
// the sweeper wipes the column once that instant passes — see
// internal/store/access_detail.go.
const CredentialTTL = 24 * time.Hour

// detailHeaderWhitelist is the whole of what headers_json may contain, in the
// order the panel reads it. Adding a header here means shipping its value into
// a table an operator can export, so anything user- or secret-shaped stays out.
var detailHeaderWhitelist = []string{"User-Agent", "X-Request-Id"}

// Capture is the per-request material a recorder hands to the logger. Every
// field is optional: an empty Capture records an empty detail row, and the
// recorder fills what it actually knows (see observe.go for status/duration/
// body, MarkCredential for tokens that only a handler can name).
type Capture struct {
	// StatusCode is the response status AT THE MOMENT THE LINE IS RECORDED.
	// Recorders that log before writing their answer (every yggdrasil handler,
	// which must not be reordered) honestly report 0 = "not decided yet";
	// recorders that log after (union inbound/outbound) report the real code.
	StatusCode int
	// DurationMS is elapsed time on the Go clock, in milliseconds.
	DurationMS int64
	// UpstreamCode names the transport/upstream failure when the recorder has
	// one ("hub.502", "dial timeout"), empty otherwise.
	UpstreamCode string
	// Headers is the request header map; only the whitelist survives Sanitized.
	Headers http.Header
	// Body is the request/response excerpt the recorder holds, pre-cap.
	Body string
	// Credential is the plaintext accessToken / web-session token. It reaches
	// the database only while the diagnostic window is open.
	Credential string
	// OutcomePending is not a column: it is the recorder telling the store
	// "the StatusCode and DurationMS above are what was true when I logged,
	// and the response has not been written yet — come back for the real
	// numbers". Attach sets it; a recorder that already knows the answer
	// (union outbound reports the hub's status) leaves it false, so the
	// hand-off never overwrites a number that was deliberately chosen.
	OutcomePending bool
}

// Sanitized returns a copy clipped to what the detail table may hold: the
// header whitelist, the body cap, and the short-column caps. It never touches
// the receiver, so a recorder can log the same capture twice.
func (c *Capture) Sanitized() *Capture {
	if c == nil {
		return nil
	}
	out := *c
	keep := make(http.Header, len(detailHeaderWhitelist))
	for _, name := range detailHeaderWhitelist {
		if v := c.Headers.Get(name); v != "" {
			keep.Set(name, cut(v, 255)) // User-Agent is a 255-byte column upstream
		}
	}
	out.Headers = keep
	// Scrub first, cap second: truncating a JSON body can cut mid-value and
	// leave something unparseable, which the scrubber would then drop whole.
	out.Body = cut(ScrubBody(c.Body), MaxBodyExcerpt)
	out.UpstreamCode = cut(c.UpstreamCode, MaxUpstreamCode)
	out.Credential = cut(c.Credential, MaxCredentialRef)
	return &out
}

// HeadersJSON serializes the (already whitelisted) headers as a JSON object.
// Empty string when nothing survived the whitelist — NULL and "" both mean
// "no headers", and the reader API renders "" as an absent dump.
func (c *Capture) HeadersJSON() string {
	if c == nil || len(c.Headers) == 0 {
		return ""
	}
	obj := make(map[string]string, len(detailHeaderWhitelist))
	for _, name := range detailHeaderWhitelist {
		if v := c.Headers.Get(name); v != "" {
			obj[name] = v
		}
	}
	if len(obj) == 0 {
		return ""
	}
	raw, err := json.Marshal(obj)
	if err != nil { // a map[string]string cannot fail to marshal; belt and braces
		return ""
	}
	return string(raw)
}

// Attach builds the capture for one inbound request: its headers, plus the
// status/latency/body/credential the Observe middleware has collected so far.
// Safe with no middleware in the chain (and with a nil request) — the recorder
// then reports a header-only row, which is still the row that answers "which
// launcher sent this".
func Attach(r *http.Request) *Capture {
	c := &Capture{}
	if r == nil {
		return c
	}
	c.Headers = r.Header
	if l := liveOf(r.Context()); l != nil {
		c.StatusCode, c.DurationMS, c.Body, c.Credential = l.snapshot()
		// 0 from the slot means the status line has not been written yet, and
		// Observe is on this request — so the real numbers are still to come.
		// Without Observe there is nobody to hand them back, and the row keeps
		// what the recorder saw instead of waiting for an update that never
		// arrives.
		c.OutcomePending = c.StatusCode == 0
	}
	return c
}

// MarkCredential records the bearer credential a handler decoded out of the
// request (yggdrasil carries the accessToken in a JSON body, so the middleware
// cannot see it). A no-op without Observe — the credential then simply is not
// captured, which is the safe direction.
//
// This is the ONLY home for a plaintext credential on the logging path, and
// Logger.Log drops it unless the diagnostic window is open. The summary row
// rules (internal/ygg/accesslog_credentials_test.go) are untouched by it.
func MarkCredential(ctx context.Context, credential string) {
	if credential == "" {
		return
	}
	if l := liveOf(ctx); l != nil {
		l.markCredential(credential)
	}
}
