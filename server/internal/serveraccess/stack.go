package serveraccess

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
)

// Stack owns the live module gate. It has no store, key material or workers:
// disabling the module cannot write data, generate credentials or start work.
type Stack struct {
	on atomic.Bool
}

func NewStack(cfg Config) *Stack {
	s := &Stack{}
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

// Handler is the complete Slice S1 API. Unknown routes and all methods other
// than these two GETs remain 404/405; no control or verification policy exists.
func (s *Stack) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/serveraccess/v1/health", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/serveraccess/v1/servers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"servers": []any{}})
	})
	return mux
}
