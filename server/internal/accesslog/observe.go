// Per-request observation (ticket 5c).
//
// Observe remembers, per request, the three things a recorder cannot know at
// the moment it logs: the status written so far, the elapsed time, and a
// bounded prefix of the request body. It is not a policy component — it reads
// no options and decides nothing about the diagnostic window (that is
// Logger.Log), which is what lets it sit in front of every request for the cost
// of one mutex-guarded counter and at most MaxBodyExcerpt bytes copied.
//
// Frozen-surface note: the wrapper forwards Write and WriteHeader verbatim and
// returns their results untouched; it does not buffer a response, add a header,
// or swallow an error, so /api/yggdrasil answers exactly what it answered
// before. It deliberately does NOT promote http.Hijacker (only http.Flusher is
// forwarded, which is what the gzip writer in cmd/ does too).
package accesslog

import (
	"context"
	"io"
	"net/http"
	"sync"
	"time"
)

// observeNow is the request clock. Injectable for tests (same seam idea as
// Diagnostic.Now): captured_at and duration_ms must come from the Go clock,
// never from SQL now().
var observeNow = time.Now

// live carries one request's observation. It is reachable from the request
// context so any depth of handler can attach to it, and it is written under a
// mutex because a handler may log from a goroutine it spawned.
type live struct {
	mu          sync.Mutex
	start       time.Time
	status      int
	wroteHeader bool
	body        []byte
	credential  string
	finalizers  []func(status int, durationMS int64)
}

type liveContextKey struct{}

// snapshot reports what has happened on this request so far.
func (l *live) snapshot() (status int, durationMS int64, body, credential string) {
	if l == nil {
		return 0, 0, "", ""
	}
	now := observeNow()
	l.mu.Lock()
	defer l.mu.Unlock()
	d := now.Sub(l.start).Milliseconds()
	if d < 0 {
		d = 0 // a clock that went backwards must not poison the row
	}
	return l.status, d, string(l.body), l.credential
}

func (l *live) writeHeader(code int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.wroteHeader {
		l.status, l.wroteHeader = code, true
	}
}

// noteBody keeps the leading MaxBodyExcerpt bytes and drops the rest: the
// excerpt is a debugging aid, not an archive, and an unbounded tee would let a
// 2 MiB skin upload grow the logging path's memory.
func (l *live) noteBody(p []byte) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(l.body) >= MaxBodyExcerpt || len(p) == 0 {
		return
	}
	if free := MaxBodyExcerpt - len(l.body); len(p) > free {
		p = p[:free]
	}
	l.body = append(l.body, p...)
}

func (l *live) markCredential(credential string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.credential = credential
}

// finalize is the post-response hand-off (2026-11-09 rig finding). The recorder
// logs when the handler has decided the outcome — before the response line is
// written — so a detail row can only learn the real status and the full
// duration here, after the wrapped handler returns. Status inference follows
// net/http: a handler that returns without writing anything gets an implicit
// 200, while a handler that panics without writing is left at 0 because the
// recovery above this wrapper, not this wrapper, decides what that becomes.
// A finalizer registered after this point never runs; the row then honestly
// keeps the pre-response values.
func (l *live) finalize(panicked bool) {
	now := observeNow()
	l.mu.Lock()
	d := now.Sub(l.start).Milliseconds()
	if d < 0 {
		d = 0
	}
	status := l.status
	if !l.wroteHeader && !panicked {
		status = http.StatusOK
	}
	fs := l.finalizers
	l.finalizers = nil
	l.mu.Unlock()
	for _, f := range fs {
		f(status, d)
	}
}

// OnResponseEnd registers fn to run once this request's response is finished,
// with the final status code and the full duration. It is a no-op without
// Observe (no slot) or with a nil argument, so the logging path can call it
// unconditionally.
func OnResponseEnd(ctx context.Context, fn func(status int, durationMS int64)) {
	if ctx == nil || fn == nil {
		return
	}
	l := liveOf(ctx)
	if l == nil {
		return
	}
	l.mu.Lock()
	l.finalizers = append(l.finalizers, fn)
	l.mu.Unlock()
}

// liveOf returns the observation slot for this request (nil = no middleware).
func liveOf(ctx context.Context) *live {
	if ctx == nil {
		return nil
	}
	l, _ := ctx.Value(liveContextKey{}).(*live)
	return l
}

// Observe installs the per-request observation slot on the context and wraps
// the response writer to note the status code.
func Observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if next == nil {
			return
		}
		l := &live{start: observeNow()}
		// Only requests that claim a body get a tee; ContentLength 0 covers a
		// plain GET (and http.NoBody), which has nothing to excerpt. Unknown
		// length (-1, chunked) is wrapped, since that is a body.
		if r.Body != nil && r.ContentLength != 0 {
			r.Body = &teeReader{ReadCloser: r.Body, live: l}
		}
		// The deferred closure, not a deferred method call: `panicked` must be
		// read when the panic is already on its way, not when the defer is
		// registered.
		panicked := true
		defer func() { l.finalize(panicked) }()
		next.ServeHTTP(&statusWriter{ResponseWriter: w, live: l},
			r.WithContext(context.WithValue(r.Context(), liveContextKey{}, l)))
		panicked = false
	})
}

// statusWriter notes the first status written and forwards everything.
type statusWriter struct {
	http.ResponseWriter
	live *live
}

func (w *statusWriter) WriteHeader(code int) {
	w.live.writeHeader(code)
	w.ResponseWriter.WriteHeader(code)
}

// Write stamps the implicit 200 (net/http semantics) and forwards the bytes.
// writeHeader ignores every call after the first, so an explicit WriteHeader
// ahead of this one keeps its code.
func (w *statusWriter) Write(b []byte) (int, error) {
	w.live.writeHeader(http.StatusOK)
	return w.ResponseWriter.Write(b)
}

// Flush forwards the flusher capability the gzip writer in cmd/ also forwards.
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// teeReader passes the body through untouched and copies its prefix.
type teeReader struct {
	io.ReadCloser
	live *live
}

func (t *teeReader) Read(p []byte) (int, error) {
	n, err := t.ReadCloser.Read(p)
	if n > 0 {
		t.live.noteBody(p[:n])
	}
	return n, err
}
