package accesslog

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// The recorder logs at the moment the handler decides the outcome, which is
// BEFORE the response line is written. Without a post-response hand-off the
// detail row can only ever store status 0 and a half-measured duration — the
// two columns the panel shows an operator. These tests pin the hand-off.

func TestObserveHandsTheFinalOutcomeToARegisteredCallback(t *testing.T) {
	var gotStatus int
	var gotDuration int64
	var called int

	h := Observe(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Mid-request: this is what the sink does right after inserting the row.
		OnResponseEnd(r.Context(), func(status int, durationMS int64) {
			called++
			gotStatus, gotDuration = status, durationMS
		})
		time.Sleep(12 * time.Millisecond)
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/yggdrasil/authserver/authenticate", nil))

	if rec.Code != http.StatusTeapot {
		t.Fatalf("response status = %d, want 418 — the wrapper must not alter the response", rec.Code)
	}
	if called != 1 {
		t.Fatalf("finalizer calls = %d, want 1", called)
	}
	if gotStatus != http.StatusTeapot {
		t.Fatalf("finalizer saw status %d, want 418 — the row would keep status_code 0", gotStatus)
	}
	if gotDuration < 12 {
		t.Fatalf("finalizer saw duration %dms, want >= 12 — that is a half-measured request", gotDuration)
	}
}

// A handler that returns without writing anything still gets a 200 on the wire
// (net/http's implicit status), so the row may honestly say 200. A handler that
// panics before writing must NOT: the recovery above this wrapper decides that
// response, so the honest value there is 0 = not observed.
func TestObserveFinalizerStatusWithoutAnExplicitWrite(t *testing.T) {
	t.Run("quiet return is the implicit 200", func(t *testing.T) {
		var gotStatus = -1
		h := Observe(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			OnResponseEnd(r.Context(), func(status int, _ int64) { gotStatus = status })
		}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
		if gotStatus != http.StatusOK {
			t.Fatalf("finalizer saw status %d, want 200", gotStatus)
		}
	})
	t.Run("panic before any write is not observed", func(t *testing.T) {
		var gotStatus = -1
		defer func() {
			_ = recover()
			if gotStatus != 0 {
				t.Fatalf("finalizer saw status %d, want 0 — nothing was written and net/http, not this wrapper, produced the 500", gotStatus)
			}
		}()
		h := Observe(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			OnResponseEnd(r.Context(), func(status int, _ int64) { gotStatus = status })
			panic("boom before the status line")
		}))
		h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/x", nil))
	})
}

// Without the middleware there is no observation to hand off; registration must
// be a no-op instead of a panic, because the logging path runs on every request
// and tests/places that call Logger.Log directly have no Observe.
func TestOnResponseEndWithoutObserveIsANoop(t *testing.T) {
	OnResponseEnd(context.Background(), func(int, int64) {
		t.Fatal("must not run without Observe")
	})
	OnResponseEnd(nil, func(int, int64) { t.Fatal("must not run on a nil context") })
}

// A panicking handler still gets its outcome reported when the status was
// already written; the row must not silently keep the pre-response values.
func TestObserveFinalizerRunsEvenIfTheHandlerPanics(t *testing.T) {
	var gotStatus int
	defer func() {
		if p := recover(); p == nil {
			t.Fatal("expected the handler panic to propagate")
		}
		if gotStatus != http.StatusOK {
			t.Fatalf("finalizer saw status %d, want 200 written before the panic", gotStatus)
		}
	}()
	h := Observe(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		OnResponseEnd(r.Context(), func(status int, _ int64) { gotStatus = status })
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("partial"))
		panic("boom")
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/y", nil))
}

// The credential deadline is a separate concern from the outcome, but both ride
// the same row: marking a credential mid-request must still be visible to the
// finalizer's snapshot path (regression guard for the ordering of the two).
func TestMarkCredentialSurvivesUntilFinalize(t *testing.T) {
	var body string
	h := Observe(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		MarkCredential(r.Context(), "abc123")
		c := Attach(r)
		body = c.Credential
		w.WriteHeader(http.StatusOK)
	}))
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/z", nil))
	if body != "abc123" {
		t.Fatalf("mid-request Attach saw credential %q, want abc123", body)
	}
}
