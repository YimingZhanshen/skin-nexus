package adminapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/plugin"
)

type adminMemStore struct {
	opts       map[string]string
	configs    map[string][]byte
	secrets    map[string]pluginSecret
	kvs        map[string]plugin.KVRecord
	docs       map[string]plugin.DocRecord
	migrations map[string]int
}

type pluginSecret struct{ ct, nonce []byte }

func (m *adminMemStore) GetOption(_ context.Context, k string) (string, bool, error) {
	v, ok := m.opts[k]
	return v, ok, nil
}
func (m *adminMemStore) SetOption(_ context.Context, k, v string) error { m.opts[k] = v; return nil }
func (m *adminMemStore) GetPluginConfig(_ context.Context, n string) ([]byte, bool, error) {
	v, ok := m.configs[n]
	return v, ok, nil
}
func (m *adminMemStore) SetPluginConfig(_ context.Context, n string, v []byte) error {
	m.configs[n] = append([]byte(nil), v...)
	return nil
}
func (m *adminMemStore) GetPluginSecret(_ context.Context, p, k string) ([]byte, []byte, int, bool, error) {
	s, ok := m.secrets[p+"/"+k]
	return s.ct, s.nonce, 1, ok, nil
}
func (m *adminMemStore) SetPluginSecret(_ context.Context, p, k string, ct, nonce []byte, _ int) error {
	m.secrets[p+"/"+k] = pluginSecret{ct: ct, nonce: nonce}
	return nil
}
func (m *adminMemStore) DeletePluginSecret(_ context.Context, p, k string) error {
	delete(m.secrets, p+"/"+k)
	return nil
}
func (m *adminMemStore) UpsertPluginMeta(_ context.Context, _ plugin.PluginMetaRecord) error {
	return nil
}
func (m *adminMemStore) GetPluginMeta(_ context.Context, _ string) (plugin.PluginMetaRecord, bool, error) {
	return plugin.PluginMetaRecord{}, false, nil
}
func (m *adminMemStore) UpdatePluginState(_ context.Context, _, _, _ string) error { return nil }

func newConfigAdminBackend(t *testing.T) *Backend {
	t.Helper()
	root := t.TempDir()
	dir := filepath.Join(root, "cfg")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := `{
	  "name":"cfg","version":"1","hookApi":2,"entrypoint":"index.js",
	  "permissions":["config"],
	  "config":{"schema":{"type":"object","required":["token"],"properties":{
	    "name":{"type":"string","default":"hello"},
	    "token":{"type":"string","format":"secret"}
	  }}}
	}`
	if err := os.WriteFile(filepath.Join(dir, "plugin.json"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "index.js"), []byte("skin.log('x');"), 0o644); err != nil {
		t.Fatal(err)
	}
	box, err := plugin.NewSecretBox("", filepath.Join(root, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	st := &adminMemStore{opts: map[string]string{}, configs: map[string][]byte{}, secrets: map[string]pluginSecret{}, kvs: map[string]plugin.KVRecord{}, docs: map[string]plugin.DocRecord{}, migrations: map[string]int{}}
	host := plugin.NewHost(plugin.RuntimeConfig{Dir: root, HostVersion: "0.4.0", Store: st, SecretBox: box})
	t.Cleanup(func() { _ = host.Close() })
	if err := host.Load(root); err != nil {
		t.Fatal(err)
	}
	b := &Backend{AdminTok: "sekret"}
	b.MountPlugins(PluginsDeps{Host: host})
	return b
}

func configReq(t *testing.T, b *Backend, actor int64, method, body string) *httptest.ResponseRecorder {
	t.Helper()
	stubSession(t, func(*http.Request) (int64, bool) {
		if actor == 0 {
			return 0, false
		}
		return actor, true
	})
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, "/api/admin/v1/plugins/cfg/config", nil)
	} else {
		r = httptest.NewRequest(method, "/api/admin/v1/plugins/cfg/config", strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	if actor == 0 {
		r.Header.Set("X-Admin-Token", "sekret")
	} else {
		r.Header.Set("Origin", "http://example.com")
	}
	rec := httptest.NewRecorder()
	b.Handler().ServeHTTP(rec, r)
	return rec
}

func TestPluginConfigAdminAuthAndSave(t *testing.T) {
	b := newConfigAdminBackend(t)
	stubPerm(t, 2)

	rec := configReq(t, b, 5, http.MethodGet, "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"hello"`) {
		t.Fatalf("get = %d %s", rec.Code, rec.Body.String())
	}
	if rec := configReq(t, b, 0, http.MethodPut, `{"values":{"token":"x"}}`); rec.Code != http.StatusForbidden {
		t.Fatalf("token write = %d", rec.Code)
	}
	stubPerm(t, 1)
	if rec := configReq(t, b, 5, http.MethodPut, `{"values":{"token":"x"}}`); rec.Code != http.StatusForbidden {
		t.Fatalf("admin write = %d", rec.Code)
	}
	stubPerm(t, 2)
	rec = configReq(t, b, 5, http.MethodPut, `{"values":{"name":"site","token":"x"}}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("super write = %d %s", rec.Code, rec.Body.String())
	}
	var view map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &view)
	values := view["values"].(map[string]any)
	if values["token"] != "********" || values["name"] != "site" {
		t.Fatalf("masked = %v", values)
	}
	if rec := configReq(t, b, 5, http.MethodPut, `{"values":{"name":3,"token":"x"}}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("invalid = %d %s", rec.Code, rec.Body.String())
	}
}

func (m *adminMemStore) GetPluginKV(_ context.Context, p, ns, k string) (string, *time.Time, bool, error) {
	r, ok := m.kvs[p+"/"+ns+"/"+k]
	return r.Value, r.ExpiresAt, ok, nil
}
func (m *adminMemStore) SetPluginKV(_ context.Context, p, ns, k, v string, exp *time.Time) error {
	m.kvs[p+"/"+ns+"/"+k] = plugin.KVRecord{Namespace: ns, Key: k, Value: v, ExpiresAt: exp}
	return nil
}
func (m *adminMemStore) DeletePluginKV(_ context.Context, p, ns, k string) error {
	delete(m.kvs, p+"/"+ns+"/"+k)
	return nil
}
func (m *adminMemStore) ListPluginKV(_ context.Context, p, ns, prefix string, limit int) ([]plugin.KVRecord, error) {
	return nil, nil
}
func (m *adminMemStore) CountPluginKV(_ context.Context, p string) (int, error) { return 0, nil }
func (m *adminMemStore) GetPluginDoc(_ context.Context, p, coll, id string) ([]byte, bool, error) {
	d, ok := m.docs[p+"/"+coll+"/"+id]
	return d.Doc, ok, nil
}
func (m *adminMemStore) PutPluginDoc(_ context.Context, p, coll, id string, doc []byte) error {
	m.docs[p+"/"+coll+"/"+id] = plugin.DocRecord{Collection: coll, ID: id, Doc: doc}
	return nil
}
func (m *adminMemStore) DeletePluginDoc(_ context.Context, p, coll, id string) error {
	delete(m.docs, p+"/"+coll+"/"+id)
	return nil
}
func (m *adminMemStore) ListPluginDocs(_ context.Context, p, coll, prefix string, limit int) ([]plugin.DocRecord, error) {
	return nil, nil
}
func (m *adminMemStore) CountPluginDocs(_ context.Context, p, coll string) (int, error) {
	return 0, nil
}
func (m *adminMemStore) PluginMigrationLatest(_ context.Context, p string) (int, error) {
	return 0, nil
}
func (m *adminMemStore) RecordPluginMigration(_ context.Context, p string, v int) error { return nil }
