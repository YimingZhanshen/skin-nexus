package accesslog

// observe_test.go — the per-request state the detail row is built from.
//
// Observe is deliberately clueless about the window: it only remembers what
// the request did (status written so far, elapsed time, a bounded prefix of the
// body). The decision to persist any of it is TestLogDropsCaptureWhenWindowClosed's
// business, up in Logger.Log. Splitting it that way is what lets the tee stay
// installed on every request without ever reading the options table.

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestObserveFeedsStatusDurationAndBodyToAttach(t *testing.T) {
	base := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	restore := observeNow
	defer func() { observeNow = restore }()
	observeNow = func() time.Time { return base }

	var before, after *Capture
	var body string
	var flusherOK, hijackerOK bool
	h := Observe(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, flusherOK = w.(http.Flusher)
		_, hijackerOK = w.(http.Hijacker)
		b, _ := io.ReadAll(r.Body)
		body = string(b)
		MarkCredential(r.Context(), detSecret)
		before = Attach(r) // the ygg shape: the line is recorded pre-response
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("nope"))
		observeNow = func() time.Time { return base.Add(1500 * time.Millisecond) }
		after = Attach(r) // the union-inbound shape: recorded post-response
	}))

	req := httptest.NewRequest(http.MethodPost, "/api/yggdrasil/authserver/refresh",
		strings.NewReader(`{"accessToken":"`+detSecret+`","clientToken":"c1"}`))
	req.Header.Set("User-Agent", "rig/1")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if body == "" {
		t.Fatal("the middleware swallowed the request body")
	}
	if rec.Code != http.StatusServiceUnavailable || rec.Body.String() != "nope" {
		t.Errorf("response altered by the observer: status=%d body=%q", rec.Code, rec.Body.String())
	}
	if !flusherOK {
		t.Error("the wrapped ResponseWriter lost http.Flusher")
	}
	if hijackerOK {
		t.Error("the wrapped ResponseWriter must not claim http.Hijacker it cannot forward")
	}

	if before == nil || before.Body != body {
		t.Errorf("capture before the response = %+v, want the request body %q", before, body)
	}
	if before == nil || before.StatusCode != 0 {
		t.Errorf("status recorded before WriteHeader = %v, want 0 (unknown), not a guess", before)
	}
	if after == nil || after.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status recorded after the response = %+v, want 503", after)
	}
	if after == nil || after.DurationMS != 1500 {
		t.Errorf("duration = %+v, want 1500ms off the injected clock", after)
	}
	if after == nil || after.Credential != detSecret {
		t.Errorf("MarkCredential did not reach the capture: %+v", after)
	}
	if before != nil && before.Credential != detSecret {
		t.Errorf("credential missing on the pre-response capture: %+v", before)
	}
}

// A body larger than the cap must not be buffered in full.
func TestObserveCapsTheBodyTee(t *testing.T) {
	var got *Capture
	h := Observe(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		if len(b) != MaxBodyExcerpt*3 {
			t.Errorf("handler received %d bytes, want the whole body forwarded", len(b))
		}
		got = Attach(r)
	}))
	req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader(strings.Repeat("z", MaxBodyExcerpt*3)))
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got == nil {
		t.Fatal("no capture recorded")
	}
	if len(got.Body) != MaxBodyExcerpt {
		t.Errorf("teeed body = %d bytes, want the cap %d", len(got.Body), MaxBodyExcerpt)
	}
}

// A GET with no body must stay a no-op (no wrapper, no allocation, no panic).
func TestObserveLeavesBodylessRequestsAlone(t *testing.T) {
	var err error
	h := Observe(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, rerr := io.ReadAll(r.Body)
		err = rerr
		if len(b) != 0 {
			t.Errorf("empty body read as %d bytes", len(b))
		}
		if c := Attach(r); c == nil || c.Body != "" {
			t.Errorf("capture for a bodyless GET = %+v", c)
		}
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	if err != nil {
		t.Errorf("reading the untouched body failed: %v", err)
	}
}

// MarkCredential must be visible to the recorder that runs later in the same
// request, and invisible to the next one.
func TestMarkCredentialIsPerRequest(t *testing.T) {
	var first, second *Capture
	h := Observe(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c := r.Header.Get("X-Token"); c != "" {
			MarkCredential(r.Context(), c)
		}
		switch r.Header.Get("X-Round") {
		case "1":
			first = Attach(r)
		default:
			second = Attach(r)
		}
	}))
	req1 := httptest.NewRequest(http.MethodPost, "/x", nil)
	req1.Header.Set("X-Round", "1")
	req1.Header.Set("X-Token", detSecret)
	h.ServeHTTP(httptest.NewRecorder(), req1)
	req2 := httptest.NewRequest(http.MethodPost, "/x", nil)
	req2.Header.Set("X-Round", "2")
	h.ServeHTTP(httptest.NewRecorder(), req2)

	if first == nil || first.Credential != detSecret {
		t.Errorf("round 1 credential = %+v", first)
	}
	if second == nil || second.Credential != "" {
		t.Errorf("round 2 inherited a credential: %+v", second)
	}
}
