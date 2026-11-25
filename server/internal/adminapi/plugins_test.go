package adminapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"skin-nexus/server/internal/plugin"
)

// newPluginBackend builds an admin Backend wired to a temp plugin host.
func newPluginBackend(t *testing.T) (*Backend, *plugin.Manager) {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"demo","version":"1.0.0","hookApi":2,"entrypoint":"index.js","permissions":["textures:read"]}`
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte(`skin.log("ready");`), 0o644); err != nil {
		t.Fatal(err)
	}
	host := plugin.NewHost(plugin.RuntimeConfig{Dir: root, HostVersion: "0.3.0"})
	t.Cleanup(func() { _ = host.Close() })
	if err := host.Load(root); err != nil {
		t.Fatal(err)
	}
	b := &Backend{AdminTok: "sekret"}
	b.MountPlugins(PluginsDeps{Host: host})
	return b, host
}

// stubPerm overrides the permission seam for one test.
func stubPerm(t *testing.T, perm int) {
	t.Helper()
	old := actorPermission
	actorPermission = func(context.Context, *Backend, int64) (int, error) { return perm, nil }
	t.Cleanup(func() { actorPermission = old })
}

func pluginAction(t *testing.T, b *Backend, actor int64, name, action string) *httptest.ResponseRecorder {
	t.Helper()
	stubSession(t, func(*http.Request) (int64, bool) {
		if actor == 0 {
			return 0, false
		}
		return actor, true
	})
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/api/admin/v1/plugins/"+name+"/"+action, nil)
	if actor == 0 {
		r.Header.Set("X-Admin-Token", "sekret")
	} else {
		r.Header.Set("Origin", "http://example.com")
	}
	b.Handler().ServeHTTP(rec, r)
	return rec
}

func TestPluginListEmptyAndPopulated(t *testing.T) {
	b, _ := newPluginBackend(t)
	stubSession(t, func(*http.Request) (int64, bool) { return 5, true })
	rec := httptest.NewRecorder()
	b.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/admin/v1/plugins", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("code = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"name":"demo"`) || !strings.Contains(rec.Body.String(), `"state":"disabled"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestPluginEnableRequiresSuperAdminSession(t *testing.T) {
	b, host := newPluginBackend(t)

	// X-Admin-Token path refused
	if rec := pluginAction(t, b, 0, "demo", "enable"); rec.Code != http.StatusForbidden {
		t.Fatalf("token path code = %d", rec.Code)
	}
	// regular admin refused
	stubPerm(t, 1)
	if rec := pluginAction(t, b, 5, "demo", "enable"); rec.Code != http.StatusForbidden {
		t.Fatalf("perm=1 code = %d", rec.Code)
	}
	// super admin allowed
	stubPerm(t, 2)
	if rec := pluginAction(t, b, 5, "demo", "enable"); rec.Code != http.StatusOK {
		t.Fatalf("perm=2 code = %d body=%s", rec.Code, rec.Body.String())
	}
	if len(host.List()) != 1 || !host.List()[0].Enabled {
		t.Fatalf("host state = %+v", host.List())
	}
	if rec := pluginAction(t, b, 5, "demo", "disable"); rec.Code != http.StatusOK {
		t.Fatalf("disable code = %d", rec.Code)
	}
	if host.List()[0].Enabled {
		t.Fatal("still enabled after disable")
	}
}

func TestPluginUnknownActionAndName(t *testing.T) {
	b, _ := newPluginBackend(t)
	stubPerm(t, 2)
	if rec := pluginAction(t, b, 5, "demo", "explode"); rec.Code != http.StatusNotFound {
		t.Fatalf("action code = %d", rec.Code)
	}
	if rec := pluginAction(t, b, 5, "nope", "enable"); rec.Code != http.StatusNotFound {
		t.Fatalf("name code = %d", rec.Code)
	}
}
