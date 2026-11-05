package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const uiManifest = `{
  "name": "ui",
  "version": "1.0.0",
  "hookApi": 2,
  "entrypoint": "index.js",
  "permissions": ["ui:page", "ui:slot", "ui:assets"],
  "ui": {
    "assets": "ui/",
    "pages": [{"id": "home", "path": "/ext/ui/home", "area": "user", "nav": {"label": "UI Home"}, "render": {"type": "iframe", "src": "home.html"}, "perm": 0}],
    "slots": [{"id": "skinlib.detail.sidebar", "render": {"type": "iframe", "src": "slot.html"}, "height": 120}],
    "i18n": {"zh-CN": {"title": "界面"}, "en": {"title": "UI"}}
  }
}`

func TestUIMetadataAssetsAndSlots(t *testing.T) {
	root := t.TempDir()
	dir := writePlugin(t, root, "ui", uiManifest, "skin.log('ui');")
	if err := os.MkdirAll(filepath.Join(dir, "ui"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ui", "home.html"), []byte("<h1>home</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "ui", "slot.html"), []byte("<p>slot</p>"), 0o644); err != nil {
		t.Fatal(err)
	}
	host := NewHost(RuntimeConfig{Dir: root, HostVersion: "0.4.0"})
	t.Cleanup(func() { _ = host.Close() })
	if err := host.Load(root); err != nil {
		t.Fatal(err)
	}
	if err := host.Enable(context.Background(), "ui"); err != nil {
		t.Fatal(err)
	}
	ui := host.UI()
	if len(ui) != 1 || len(ui[0].Pages) != 1 || len(ui[0].Slots) != 1 {
		t.Fatalf("ui = %+v", ui)
	}
	if !host.HasUIPath("ext/ui/home") || host.HasUIPath("ext/ui/nope") {
		t.Fatal("HasUIPath mismatch")
	}
	rec := httptest.NewRecorder()
	host.AssetHandler(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/plugin-assets/ui/home.html", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "home") {
		t.Fatalf("asset = %d %s", rec.Code, rec.Body.String())
	}
	rec = httptest.NewRecorder()
	host.AssetHandler(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/plugin-assets/ui/../plugin.json", nil))
	if rec.Code != 404 {
		t.Fatalf("traversal = %d", rec.Code)
	}
	html := host.SlotHTML("skinlib.detail.sidebar")
	if !strings.Contains(html, "/plugin-assets/ui/slot.html") || !strings.Contains(html, "min-height:120px") {
		t.Fatalf("slot html = %s", html)
	}
	html = host.SlotHTMLContext("skinlib.detail.sidebar", map[string]string{"hash": "abc", "tid": "7", "empty": ""})
	if !strings.Contains(html, "slot.html?hash=abc&amp;tid=7") {
		t.Fatalf("slot context = %s", html)
	}
	if err := host.Disable(context.Background(), "ui"); err != nil {
		t.Fatal(err)
	}
	if len(host.UI()) != 0 || host.HasUIPath("ext/ui/home") {
		t.Fatal("disabled plugin still exposes UI")
	}
	rec = httptest.NewRecorder()
	host.AssetHandler(nil).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/plugin-assets/ui/home.html", nil))
	if rec.Code != 404 {
		t.Fatalf("disabled asset = %d", rec.Code)
	}
}
