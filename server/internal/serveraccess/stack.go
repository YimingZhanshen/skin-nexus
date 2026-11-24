package serveraccess

import (
	"net/http"
	"sync/atomic"
)

// Stack owns the live module gate. It has no store, key material or workers:
// disabling the module cannot write data, generate credentials or start work.
type Stack struct {
	on  atomic.Bool
	cfg Config
}

func NewStack(cfg Config) *Stack {
	s := &Stack{cfg: cfg}
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

// Gate rejects the entire mounted subtree while disabled, before the next
// handler can expose a surface or fall through to the SPA.
func (s *Stack) Gate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.Enabled() {
			http.NotFound(w, r)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Handler mounts the Slice S2 control plane. Authentication and resource scope
// are enforced per route; the outer Gate remains the module fail-closed switch.
func (s *Stack) Handler() http.Handler {
	return s.controlHandler()
}
