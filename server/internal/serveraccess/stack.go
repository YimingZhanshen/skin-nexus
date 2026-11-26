package serveraccess

import (
	"context"
	"errors"
	"net/http"
	"sync/atomic"

	"skin-nexus/server/internal/ygg"
)

// Stack owns the live module gate and immutable control-plane dependencies.
// It generates no keys and starts no workers at assembly; a disabled gate
// prevents every request from reaching the store.
type Stack struct {
	on        atomic.Bool
	cfg       Config
	retention *RetentionWorker
}

func NewStack(cfg Config) *Stack {
	s := &Stack{cfg: cfg}
	s.retention = NewRetentionWorker(cfg.Store, cfg.Now, 0)
	s.SetEnabled(cfg.Enabled)
	return s
}

// Enabled is nil-safe so a failed assembly remains inert.
func (s *Stack) Enabled() bool { return s != nil && s.on.Load() }

// SetEnabled is the panel hot toggle. It only changes the request gate.
func (s *Stack) SetEnabled(on bool) {
	if s != nil {
		s.on.Store(on)
	}
}

// StartRetention is explicit and fail-closed: a disabled module or missing
// store never creates a goroutine. Repeated calls while running are harmless.
func (s *Stack) StartRetention(ctx context.Context) error {
	if s == nil || !s.Enabled() {
		return nil
	}
	if s.retention == nil || s.retention.store == nil {
		return errors.New("serveraccess: retention store is required")
	}
	return s.retention.Start(ctx)
}

func (s *Stack) StopRetention() {
	if s != nil && s.retention != nil {
		s.retention.Stop()
	}
}

func (s *Stack) RetentionRunning() bool {
	return s != nil && s.retention != nil && s.retention.Running()
}

// SetVerifier is an assembly-time injection of the narrow Yggdrasil extension.
func (s *Stack) SetVerifier(verifier *ygg.API) {
	if s != nil {
		s.cfg.Verifier = verifier
	}
}

// Gate rejects the entire mounted subtree while disabled, before the next
// handler can expose a surface or fall through to the SPA.
func (s *Stack) Gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		if !s.Enabled() {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Handler mounts the control plane and the narrow S3 verification routes. The
// outer Gate remains the module fail-closed switch for every route.
func (s *Stack) Handler() http.Handler {
	mux := http.NewServeMux()
	verify := &verifier{cfg: s.cfg, store: s.cfg.Store, concurrency: make(map[int64]*serverConcurrency)}
	verify.routes(mux)
	control := s.controlHandler()
	mux.Handle("/", control)
	return mux
}
