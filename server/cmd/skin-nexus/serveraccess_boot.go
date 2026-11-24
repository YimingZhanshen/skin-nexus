package main

import (
	"context"
	"net/http"
	"strings"
	"time"

	"skin-nexus/server/internal/accesslog"
	"skin-nexus/server/internal/config"
	"skin-nexus/server/internal/serveraccess"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/ygg"
)

// serverAccessOptionReader is deliberately read-only. Assembly can resolve
// the panel switch but cannot write options or any module table.
type serverAccessOptionReader interface {
	GetOption(context.Context, string) (string, bool, error)
}

// serverAccessStack mirrors Union's always-assembled, hot-gated lifecycle. The
// inner Stack owns the atomic request gate; its store reference stays inert
// until the gate opens and no keys or workers are created during assembly.
type serverAccessStack struct {
	stack *serveraccess.Stack
}

func (s *serverAccessStack) Enabled() bool { return s != nil && s.stack.Enabled() }

func (s *serverAccessStack) Gate(next http.Handler) http.Handler {
	if s == nil {
		return http.NotFoundHandler()
	}
	return s.stack.Gate(next)
}

func (s *serverAccessStack) setEnabled(_ context.Context, on bool) {
	if s != nil {
		s.stack.SetEnabled(on)
	}
}

func (s *serverAccessStack) startRetention(ctx context.Context) error {
	if s == nil {
		return nil
	}
	return s.stack.StartRetention(ctx)
}

func (s *serverAccessStack) stopRetention() {
	if s != nil {
		s.stack.StopRetention()
	}
}

func (s *serverAccessStack) setVerifier(verifier *ygg.API) {
	if s != nil {
		s.stack.SetVerifier(verifier)
	}
}

func newServerAccessStack(st serverAccessOptionReader, db *store.Mysql, cfg *config.Config) *serverAccessStack {
	s := &serverAccessStack{}
	enabled := cfg != nil && cfg.ServerAccessEnabled
	if st != nil {
		if raw, ok, err := st.GetOption(context.Background(), "serveraccess_enabled"); err == nil && ok && strings.TrimSpace(raw) != "" {
			// A present option is authoritative. Malformed values fail closed
			// instead of falling through to an env that may enable the module.
			switch strings.ToLower(strings.TrimSpace(raw)) {
			case "1", "true", "on", "yes":
				enabled = true
			case "0", "false", "off", "no":
				enabled = false
			default:
				enabled = false
			}
		}
	}
	grace := time.Duration(0)
	if cfg != nil && cfg.ServerAccessDeletionGraceHours > 0 {
		grace = time.Duration(cfg.ServerAccessDeletionGraceHours) * time.Hour
	}
	s.stack = serveraccess.NewStack(serveraccess.Config{Enabled: enabled, Store: db, DeletionGracePeriod: grace})
	return s
}

// guard suppresses diagnostic capture before the fail-closed module gate so
// even disabled or malformed secret paths never feed headers/body to captures.
func (s *serverAccessStack) guard(next http.Handler) http.Handler {
	gated := s.Gate(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accesslog.SuppressCapture(r.Context())
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		gated.ServeHTTP(w, r)
	})
}

// mountServerAccess claims every module API prefix before the SPA catch-all.
// The exact /servers owner portal deliberately falls through in both gate
// states so the SPA can explain default-off unavailability. The SPA's known-path
// check rejects unknown descendants, and all disabled module APIs stay non-SPA 404s.
func mountServerAccess(root *http.ServeMux, s *serverAccessStack) {
	notFound := s.guard(http.NotFoundHandler())
	root.Handle("/api/serveraccess", notFound)
	root.Handle("/api/serveraccess/", s.guard(s.stack.Handler()))
	root.Handle("/api/admin/v1/serveraccess", notFound)
	root.Handle("/api/admin/v1/serveraccess/", s.guard(s.stack.Handler()))
	root.Handle("/v", notFound)
	root.Handle("/v/", s.guard(s.stack.Handler()))
}
