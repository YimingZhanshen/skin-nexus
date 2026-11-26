package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const storeV2Manifest = `{
  "name": "store",
  "version": "1.0.0",
  "hookApi": 2,
  "entrypoint": "index.js",
  "permissions": ["http:routes", "store:kv", "store:docs"],
  "store": {"schemaVersion": 1, "collections": {"notes": {"maxRows": 10}}},
  "routes": [{"method": "GET", "path": "/notes", "tier": "public"}]
}`

const storeV2Script = `
skin.store.onInstall(function (e) {
  skin.store.kv.set("installed", String(e.to));
  skin.store.collection("notes").put("hello", {text: "world"});
});
skin.route("GET", "/notes", function () {
  return {
    installed: skin.store.kv.get("installed"),
    notes: skin.store.collection("notes").list()
  };
});
`

func TestStoreServiceAndInstallMigration(t *testing.T) {
	root := t.TempDir()
	writePlugin(t, root, "store", storeV2Manifest, storeV2Script)
	st := newMemPlatformStore()
	host := NewHost(RuntimeConfig{Dir: root, HostVersion: "0.4.0", Store: st})
	t.Cleanup(func() { _ = host.Close() })
	if err := host.Load(root); err != nil {
		t.Fatal(err)
	}
	if err := host.Enable(context.Background(), "store"); err != nil {
		t.Fatal(err)
	}
	migration, err := st.PluginMigrationLatest(context.Background(), "store")
	if err != nil || migration != 1 {
		t.Fatalf("migration = %d, err=%v", migration, err)
	}
	installed, _, ok, err := st.GetPluginKV(context.Background(), "store", "", "installed")
	if err != nil || !ok || installed != "1" {
		t.Fatalf("kv = %q, ok=%v, err=%v", installed, ok, err)
	}
	rec := httptest.NewRecorder()
	host.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/plugins/store/notes", nil))
	if rec.Code != 200 {
		t.Fatalf("route = %d %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), `"installed":"1"`) || !strings.Contains(rec.Body.String(), `"text":"world"`) {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestStoreUndeclaredCollectionAndQuota(t *testing.T) {
	root := t.TempDir()
	manifest := strings.Replace(storeV2Manifest, `"collections": {"notes": {"maxRows": 10}}`, `"collections": {}`, 1)
	script := `
skin.route("GET", "/notes", function () {
  try { skin.store.collection("missing").get("x"); } catch (e) { return {error: String(e)}; }
  return {ok: true};
});
`
	writePlugin(t, root, "store", manifest, script)
	st := newMemPlatformStore()
	host := NewHost(RuntimeConfig{Dir: root, HostVersion: "0.4.0", Store: st})
	t.Cleanup(func() { _ = host.Close() })
	_ = host.Load(root)
	if err := host.Enable(context.Background(), "store"); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	host.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/plugins/store/notes", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "not declared") {
		t.Fatalf("body = %d %s", rec.Code, rec.Body.String())
	}
}
