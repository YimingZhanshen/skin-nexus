package accesslog

// detail_test.go — ticket 5c, the writer's decision layer (no database).
//
// What is pinned here, and why it lives in this package: the accesslog logger
// is the only place that both knows the diagnostic window and hands a row to
// the store, so "closed window ⇒ nothing sensitive leaves the process" has to
// be decided BEFORE the sink sees the entry — not inside one of three sinks.
// The store re-sanitizes on the way in (defense in depth, see
// internal/store/access_detail.go); these tests cover the gate itself.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// detClock returns a clock frozen at `at`, plus nothing clever: the window
// decision must be the Go clock, never SQL now().
func detClock(at time.Time) func() time.Time { return func() time.Time { return at } }

// detOptions is a live option reader over a mutable map — flipping a value
// between two calls is what proves the option is read live, not baked in.
func detOptions(vals map[string]string) OptionReader {
	return func(_ context.Context, key string) (string, bool, error) {
		v, ok := vals[key]
		return v, ok, nil
	}
}

const detSecret = "b3d1f0a7-0000-4000-8000-aaaaaaaaaaaa"

// detCapture is one recorder's worth of request material: the two whitelisted
// headers, two headers that must never be captured, a body and a credential.
func detCapture() *Capture {
	h := http.Header{}
	h.Set("User-Agent", "launcher/3.5")
	h.Set("X-Request-Id", "req-42")
	h.Set("Cookie", "skin_session="+detSecret)
	h.Set("Authorization", "Bearer "+detSecret)
	h.Set("X-Signature", "hunter2")
	return &Capture{
		StatusCode:   http.StatusServiceUnavailable,
		DurationMS:   17,
		UpstreamCode: "up.timeout",
		Headers:      h,
		Body:         `{"error":"ForbiddenOperationException"}`,
		Credential:   detSecret,
	}
}

func detLogger(sink Sink, vals map[string]string, now time.Time) *Logger {
	l := New(sink)
	l.SetDiagnostic(&Diagnostic{Option: detOptions(vals), Now: detClock(now)})
	return l
}

// (a) window CLOSED ⇒ the sink must not receive capture material at all: no
// detail row can be built from it, and the credential never leaves the logger.
func TestLogDropsCaptureWhenWindowClosed(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	cases := map[string]map[string]string{
		"option absent":      {},
		"option empty":       {DetailUntilOption: ""},
		"window expired":     {DetailUntilOption: now.Add(-time.Second).Format(time.RFC3339)},
		"window unparseable": {DetailUntilOption: "tomorrow"},
	}
	for name, vals := range cases {
		t.Run(name, func(t *testing.T) {
			var got *Entry
			l := detLogger(func(_ context.Context, e Entry) error { got = &e; return nil }, vals, now)
			l.Log(ctx, Entry{Kind: YggRefresh, Outcome: OutcomeOK, Capture: detCapture()})
			if got == nil {
				t.Fatal("the summary row was not recorded either")
			}
			if got.Capture != nil {
				t.Errorf("capture reached the sink with the window closed: %+v", got.Capture)
			}
		})
	}

	// A logger that never had the switch wired (the zero configuration) fails
	// closed too — nothing may capture credentials by default.
	var got Entry
	l := New(func(_ context.Context, e Entry) error { got = e; return nil })
	l.Log(ctx, Entry{Kind: YggRefresh, Outcome: OutcomeOK, Capture: detCapture()})
	if got.Capture != nil {
		t.Error("capture survived a logger with no Diagnostic installed")
	}
}

// (b) window OPEN ⇒ exactly the capture the recorder handed in survives, and
// the summary fields are untouched by the new path.
func TestLogKeepsCaptureWhenWindowOpen(t *testing.T) {
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	vals := map[string]string{DetailUntilOption: now.Add(30 * time.Minute).Format(time.RFC3339)}
	var got Entry
	calls := 0
	l := detLogger(func(_ context.Context, e Entry) error { calls++; got = e; return nil }, vals, now)
	l.Log(context.Background(), Entry{Kind: YggRefresh, Outcome: OutcomeOK, Detail: "client_token=x", Capture: detCapture()})
	if calls != 1 {
		t.Fatalf("sink called %d times, want 1 (one summary row per request)", calls)
	}
	if got.Capture == nil {
		t.Fatal("capture dropped while the window was open")
	}
	if got.Detail != "client_token=x" {
		t.Errorf("summary detail changed by the detail path: %q", got.Detail)
	}
	if got.Capture.Credential != detSecret {
		t.Errorf("credential = %q, want the plaintext token the window exists to capture", got.Capture.Credential)
	}
}

// (c) only the whitelist reaches headers_json. Cookie/Authorization must not be
// captured even though the client sent them — the credential has exactly one
// home (credential_ref, gated by the window), the header dump has none.
func TestCaptureHeaderWhitelist(t *testing.T) {
	c := detCapture().Sanitized()
	raw := c.HeadersJSON()
	var got map[string]string
	if err := json.Unmarshal([]byte(raw), &got); err != nil {
		t.Fatalf("headers_json is not a JSON object (%q): %v", raw, err)
	}
	if got["User-Agent"] != "launcher/3.5" || got["X-Request-Id"] != "req-42" {
		t.Errorf("whitelisted headers lost: %v", got)
	}
	for _, ban := range []string{"Cookie", "Authorization", "X-Signature", "skin_session", detSecret, "hunter2"} {
		if strings.Contains(raw, ban) {
			t.Errorf("headers_json carries %q: %s", ban, raw)
		}
	}
	if len(got) != 2 {
		t.Errorf("headers_json has %d keys, want exactly the 2 whitelisted ones: %v", len(got), got)
	}
}

// (e) the body excerpt is capped, provably: a bigger body yields the cap's
// worth of prefix, not the whole thing.
func TestCaptureBodyCapped(t *testing.T) {
	long := strings.Repeat("a", MaxBodyExcerpt*2+7)
	c := (&Capture{Body: long}).Sanitized()
	if len(c.Body) != MaxBodyExcerpt {
		t.Fatalf("body_excerpt = %d bytes, want the cap %d", len(c.Body), MaxBodyExcerpt)
	}
	if c.Body != long[:MaxBodyExcerpt] {
		t.Error("body_excerpt is not the leading MaxBodyExcerpt bytes")
	}
	if strings.Contains(c.Body, long[MaxBodyExcerpt:]) {
		t.Error("the tail of an oversized body survived the cap")
	}
	short := "fits"
	if got := (&Capture{Body: short}).Sanitized(); got.Body != short {
		t.Errorf("a body under the cap was altered: %q", got.Body)
	}
}

// Multi-byte runes must not be split by the cap: MySQL utf8mb4 rejects an
// invalid sequence, and the failure would land on the logging path.
func TestCaptureBodyCapStaysRuneSafe(t *testing.T) {
	long := strings.Repeat("中", MaxBodyExcerpt) // 3 bytes each
	c := (&Capture{Body: long}).Sanitized()
	if len(c.Body) > MaxBodyExcerpt {
		t.Fatalf("cap exceeded: %d", len(c.Body))
	}
	if MaxBodyExcerpt%3 != 0 && strings.Count(c.Body, "中")*3 != len(c.Body) {
		t.Errorf("cap split a rune: len=%d", len(c.Body))
	}
}

// The short VARCHAR columns are clipped too, or the insert fails and the whole
// detail row is lost over a cosmetic overflow.
func TestCaptureClipsShortColumns(t *testing.T) {
	c := (&Capture{
		UpstreamCode: strings.Repeat("e", MaxUpstreamCode+40),
		Credential:   strings.Repeat("t", MaxCredentialRef+40),
	}).Sanitized()
	if len(c.UpstreamCode) != MaxUpstreamCode {
		t.Errorf("upstream_code = %d bytes, want %d", len(c.UpstreamCode), MaxUpstreamCode)
	}
	if len(c.Credential) != MaxCredentialRef {
		t.Errorf("credential_ref = %d bytes, want %d", len(c.Credential), MaxCredentialRef)
	}
}

func TestSanitizedIsNilSafeAndDoesNotMutate(t *testing.T) {
	var nilCap *Capture
	if nilCap.Sanitized() != nil {
		t.Error("(*Capture)(nil).Sanitized() must stay nil")
	}
	c := detCapture()
	bodyBefore, headerCount := c.Body, len(c.Headers)
	got := c.Sanitized()
	if len(c.Headers) != headerCount {
		t.Error("Sanitized pruned the caller's own header map")
	}
	if len(got.Headers) != 2 {
		t.Errorf("sanitized capture kept %d headers, want the 2 whitelisted ones", len(got.Headers))
	}
	if got.Body != bodyBefore {
		t.Error("Sanitized mutated the source capture")
	}
}

// HeadersJSON is empty, not "null", when nothing was sent: the reader API (5d)
// and the panel both parse this column.
func TestHeadersJSONShape(t *testing.T) {
	if got := (&Capture{}).Sanitized().HeadersJSON(); got != "" {
		t.Errorf("empty capture headers_json = %q, want empty string", got)
	}
	c := &Capture{Headers: http.Header{"User-Agent": []string{"a", "b"}}}
	raw := c.Sanitized().HeadersJSON()
	var m map[string]string
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("multi-value header must still serialize as one object: %v (%s)", err, raw)
	}
	if m["User-Agent"] != "a" {
		t.Errorf("multi-value User-Agent = %q, want the first value", m["User-Agent"])
	}
}

// Attach is what the recorders call: request headers + whatever the Observe
// middleware learned about this request so far.
func TestAttachReadsTheRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/yggdrasil/x", strings.NewReader(`{"accessToken":"x"}`))
	req.Header.Set("User-Agent", "rig/1")
	req.Header.Set("X-Request-Id", "r-9")
	req.Header.Set("Authorization", "Bearer nope")
	c := Attach(req).Sanitized()
	raw := c.HeadersJSON()
	if !strings.Contains(raw, "rig/1") || !strings.Contains(raw, "r-9") {
		t.Errorf("Attach lost the request headers: %s", raw)
	}
	if strings.Contains(raw, "nope") {
		t.Errorf("Attach leaked Authorization into headers_json: %s", raw)
	}
	if c.Body != "" || c.StatusCode != 0 {
		t.Errorf("without the middleware the capture must stay blank, got %+v", c)
	}
	if Attach(nil) == nil {
		t.Error("Attach(nil) must return a usable zero capture, not nil")
	}
}

// MarkCredential is the handler's one-line upgrade for a request whose token
// sits in a decoded body (the frozen ygg handlers). Without Observe installed
// it is a no-op — it must never panic on the request path.
func TestMarkCredentialWithoutMiddlewareIsHarmless(t *testing.T) {
	MarkCredential(context.Background(), detSecret) // no live state at all
	req := httptest.NewRequest(http.MethodPost, "/x", nil)
	MarkCredential(req.Context(), detSecret)
	if Attach(req).Credential != "" {
		t.Error("a credential marked outside the Observe middleware must not surface")
	}
}

// RetentionWindow is the sweeper's one read of the retention option. Three
// things it must never do: cache the first answer, overflow a huge option into
// a negative duration (that would delete the whole table), or fall below a day.
func TestRetentionWindowFollowsTheOptionLive(t *testing.T) {
	ctx := context.Background()
	vals := map[string]string{}
	d := &Diagnostic{Option: detOptions(vals), Now: detClock(time.Unix(0, 0).UTC())}

	if got := d.RetentionWindow(ctx); got != DefaultDetailRetentionDays*24*time.Hour {
		t.Errorf("absent option = %v, want the %d-day default", got, DefaultDetailRetentionDays)
	}
	vals[DetailRetentionOption] = "7"
	if got := d.RetentionWindow(ctx); got != 7*24*time.Hour {
		t.Errorf("option 7 = %v, want 168h (the option must be re-read, not baked in)", got)
	}
	vals[DetailRetentionOption] = "999999999"
	if got := d.RetentionWindow(ctx); got != MaxDetailRetentionDays*24*time.Hour {
		t.Errorf("absurd option = %v, want the %d-day clamp", got, MaxDetailRetentionDays)
	}
	vals[DetailRetentionOption] = "0"
	if got := d.RetentionWindow(ctx); got != DefaultDetailRetentionDays*24*time.Hour {
		t.Errorf("zero option = %v, want the default (a zero window would wipe every detail row)", got)
	}
}
