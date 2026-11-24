package main

import (
	"context"
	"net/http"
	"strings"

	"skin-nexus/server/internal/config"
	"skin-nexus/server/internal/serveraccess"
)

// serverAccessOptionReader is deliberately read-only. Slice S1 assembly can
// resolve the panel switch but cannot write options or any module table.
type serverAccessOptionReader interface {
	GetOption(context.Context, string) (string, bool, error)
}

// serverAccessStack mirrors Union's always-assembled, hot-gated lifecycle. The
// inner Stack owns the atomic request gate and has no DB, key or worker deps.
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

func newServerAccessStack(st serverAccessOptionReader, cfg *config.Config) *serverAccessStack {
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
	s.stack = serveraccess.NewStack(serveraccess.Config{Enabled: enabled})
	return s
}

// mountServerAccess claims every module prefix before the SPA catch-all. The
// not-found handler also keeps not-yet-implemented verification and portal
// paths inert while the module is enabled.
func mountServerAccess(root *http.ServeMux, s *serverAccessStack) {
	notFound := s.Gate(http.NotFoundHandler())
	root.Handle("/api/serveraccess", notFound)
	root.Handle("/api/serveraccess/", s.Gate(s.stack.Handler()))
	root.Handle("/api/admin/v1/serveraccess", notFound)
	root.Handle("/api/admin/v1/serveraccess/", notFound)
	root.Handle("/v", notFound)
	root.Handle("/v/", notFound)
	root.Handle("/servers", notFound)
	root.Handle("/servers/", notFound)
}
