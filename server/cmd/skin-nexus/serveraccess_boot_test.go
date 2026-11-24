package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"skin-nexus/server/internal/config"
)

type serverAccessOptionSpy struct {
	values map[string]string
	reads  int
}

func (s *serverAccessOptionSpy) GetOption(_ context.Context, key string) (string, bool, error) {
	s.reads++
	v, ok := s.values[key]
	return v, ok, nil
}

func TestServerAccessStackOptionsFirstAndFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name    string
		option  string
		present bool
		env     bool
		want    bool
	}{
		{name: "default off"},
		{name: "env fallback", env: true, want: true},
		{name: "option off wins over env on", option: "0", present: true, env: true},
		{name: "option on wins over env off", option: "yes", present: true, want: true},
		{name: "invalid option fails closed despite env on", option: "maybe", present: true, env: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spy := &serverAccessOptionSpy{values: map[string]string{}}
			if tc.present {
				spy.values["serveraccess_enabled"] = tc.option
			}
			s := newServerAccessStack(spy, nil, &config.Config{ServerAccessEnabled: tc.env})
			if s.Enabled() != tc.want {
				t.Fatalf("Enabled() = %v, want %v", s.Enabled(), tc.want)
			}
			if spy.reads != 1 {
				t.Fatalf("option reads = %d, want 1", spy.reads)
			}
		})
	}
}

func TestServerAccessRoutesPreemptSpaCatchAll(t *testing.T) {
	s := newServerAccessStack(nil, nil, &config.Config{})
	root := http.NewServeMux()
	mountServerAccess(root, s)
	root.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("SPA"))
	})

	paths := []string{
		"/api/serveraccess",
		"/api/serveraccess/v1/health",
		"/api/serveraccess/v1/servers",
		"/api/serveraccess/v1/unknown",
		"/api/admin/v1/serveraccess/tenants",
		"/v",
		"/v/secret/sessionserver/session/minecraft/hasJoined",
		"/servers",
		"/servers/",
	}
	for _, path := range paths {
		rec := httptest.NewRecorder()
		root.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "SPA") {
			t.Errorf("disabled GET %s = %d %q, want non-SPA 404", path, rec.Code, rec.Body.String())
		}
	}

	s.setEnabled(context.Background(), true)
	for _, path := range []string{"/api/serveraccess/v1/health", "/api/serveraccess/v1/servers", "/api/admin/v1/serveraccess/tenants"} {
		rec := httptest.NewRecorder()
		root.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		want := http.StatusUnauthorized
		if path == "/api/serveraccess/v1/health" {
			want = http.StatusOK
		} else if strings.HasPrefix(path, "/api/admin/") {
			want = http.StatusForbidden
		}
		if rec.Code != want || strings.Contains(rec.Body.String(), "SPA") {
			t.Errorf("enabled GET %s = %d %q, want module %d", path, rec.Code, rec.Body.String(), want)
		}
	}
}

func TestMainWiresServerAccessAssemblyHotToggleAndRoutes(t *testing.T) {
	raw, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	assembly := strings.Index(src, "sastack := newServerAccessStack(st, st, cfg)")
	mount := strings.Index(src, "mountServerAccess(root, sastack)")
	spa := strings.Index(src, `root.Handle("/", spa)`)
	if assembly < 0 || mount < 0 || spa < 0 {
		t.Fatalf("main.go missing server-access wiring: assembly=%d mount=%d spa=%d", assembly, mount, spa)
	}
	if assembly > mount || mount > spa {
		t.Fatalf("server-access routes must be mounted before SPA: assembly=%d mount=%d spa=%d", assembly, mount, spa)
	}
	if !strings.Contains(src, `case "serveraccess_enabled":`) || !strings.Contains(src, "sastack.setEnabled(context.Background(), value == \"1\")") {
		t.Fatal("main.go does not hot-apply serveraccess_enabled")
	}
	if !strings.Contains(src, `"serveraccess_enabled":`) || !strings.Contains(src, "boolStr(cfg.ServerAccessEnabled)") {
		t.Fatal("main.go does not expose the env fallback in settings defaults")
	}
}
