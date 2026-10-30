package plugin

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
)

type fakeAccount struct {
	uid    int64
	name   string
	png    []byte
	limits SiteLimits
}

func (f *fakeAccount) Account(_ context.Context, uid int64) (AccountView, error) {
	f.uid = uid
	return AccountView{ID: uid, Nickname: "launcher"}, nil
}
func (f *fakeAccount) Limits(context.Context) (SiteLimits, error) { return f.limits, nil }
func (f *fakeAccount) ListPlayers(_ context.Context, uid int64) ([]PlayerView, error) {
	f.uid = uid
	return []PlayerView{{ID: 1, Name: "steve"}}, nil
}
func (f *fakeAccount) GetPlayer(_ context.Context, uid, pid int64) (PlayerView, error) {
	f.uid = uid
	if pid != 1 {
		return PlayerView{}, &AccountError{Code: "player.not_found", Message: "missing"}
	}
	return PlayerView{ID: 1, Name: "steve"}, nil
}
func (f *fakeAccount) CreatePlayer(_ context.Context, uid int64, name string, _ AccountCallMeta) (PlayerView, bool, error) {
	f.uid, f.name = uid, name
	return PlayerView{ID: 2, Name: name}, true, nil
}
func (f *fakeAccount) RenamePlayer(_ context.Context, uid, pid int64, name string, _ AccountCallMeta) (PlayerView, bool, error) {
	f.uid, f.name = uid, name
	return PlayerView{ID: pid, Name: name}, true, nil
}
func (f *fakeAccount) DeletePlayer(_ context.Context, uid, _ int64, _ AccountCallMeta) error {
	f.uid = uid
	return nil
}
func (f *fakeAccount) SetTexture(_ context.Context, uid, pid int64, _ string, _ int64, _ AccountCallMeta) (PlayerView, error) {
	f.uid = uid
	return PlayerView{ID: pid}, nil
}
func (f *fakeAccount) ListTextures(_ context.Context, uid int64) ([]TextureView, error) {
	f.uid = uid
	return nil, nil
}
func (f *fakeAccount) UploadTexture(_ context.Context, uid int64, _, _, _ string, png []byte, _ AccountCallMeta) (TextureView, bool, error) {
	f.uid, f.png = uid, png
	return TextureView{ID: 3, Hash: "abc"}, true, nil
}
func (f *fakeAccount) ImportPrivateTexture(_ context.Context, uid int64, _, _, _ string, png []byte, _ AccountCallMeta) (TextureView, bool, error) {
	f.uid, f.png = uid, png
	return TextureView{ID: 3, Hash: "abc"}, true, nil
}
func (f *fakeAccount) ListLibrary(_ context.Context, uid int64, _ LibraryQuery) (LibraryPage, error) {
	f.uid = uid
	return LibraryPage{}, nil
}
func (f *fakeAccount) GetLibrary(_ context.Context, uid, _ int64) (LibraryItem, error) {
	f.uid = uid
	return LibraryItem{}, nil
}
func (f *fakeAccount) ImportLibrary(_ context.Context, uid, tid int64, _ AccountCallMeta) (TextureView, bool, error) {
	f.uid = uid
	return TextureView{ID: tid}, true, nil
}

const accountBridgeManifest = `{
  "name": "bridge",
  "version": "1.0.0",
  "hookApi": 2,
  "entrypoint": "index.js",
  "permissions": ["http:routes:bearer", "account:read", "players:read", "players:write", "textures:read", "textures:write"],
  "routes": [
    {"method": "GET", "path": "/who", "tier": "bearer"},
    {"method": "POST", "path": "/create", "tier": "bearer"},
    {"method": "POST", "path": "/upload", "tier": "bearer", "bodyLimit": 4194304, "bodyEncoding": "base64"}
  ]
}`

const accountBridgeScript = `
skin.route("GET", "/who", function () { return skin.account.get(); });
skin.route("POST", "/create", function (r) { return skin.players.create(r.body.name); });
skin.route("POST", "/upload", function (r) { return skin.textures.uploadBase64("skin", "slim", "d", r.body); });
`

func newAccountBridgeHost(t *testing.T, fa *fakeAccount) *Manager {
	t.Helper()
	root := t.TempDir()
	writePlugin(t, root, "bridge", accountBridgeManifest, accountBridgeScript)
	m := NewHost(RuntimeConfig{Dir: root, HostVersion: "0.4.0", BearerResolver: fakeBearerResolver{}, Account: fa})
	t.Cleanup(func() { _ = m.Close() })
	if err := m.Load(root); err != nil {
		t.Fatal(err)
	}
	if err := m.Enable(context.Background(), "bridge"); err != nil {
		t.Fatal(err)
	}
	return m
}

func bearerReqPath(m *Manager, method, path, auth, contentType, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Host = "skin.test"
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, r)
	return rec
}

func TestAccountBridgeBindsRouteIdentity(t *testing.T) {
	fa := &fakeAccount{}
	m := newAccountBridgeHost(t, fa)

	rec := bearerReqPath(m, "GET", "/api/plugins/bridge/who", "Bearer good", "", "")
	if rec.Code != 200 || fa.uid != 9 {
		t.Fatalf("who = %d %s uid=%d", rec.Code, rec.Body.String(), fa.uid)
	}

	rec = bearerReqPath(m, "POST", "/api/plugins/bridge/create", "Bearer good", "application/json", `{"name":"alex"}`)
	if rec.Code != 200 || fa.uid != 9 || fa.name != "alex" {
		t.Fatalf("create = %d %s uid=%d name=%q", rec.Code, rec.Body.String(), fa.uid, fa.name)
	}
}

func TestAccountBridgeUploadDecodesBase64(t *testing.T) {
	fa := &fakeAccount{}
	m := newAccountBridgeHost(t, fa)
	raw := []byte{0x89, 'P', 'N', 'G', 0x00, 0xff}
	rec := bearerReqPath(m, "POST", "/api/plugins/bridge/upload", "Bearer good", "image/png", string(raw))
	if rec.Code != 200 || string(fa.png) != string(raw) {
		t.Fatalf("upload = %d %s png=%v", rec.Code, rec.Body.String(), fa.png)
	}
}

func TestAccountBridgeRequiresIdentity(t *testing.T) {
	fa := &fakeAccount{}
	m := newAccountBridgeHost(t, fa)
	rec := bearerReqPath(m, "GET", "/api/plugins/bridge/who", "", "", "")
	if rec.Code != 401 {
		t.Fatalf("anonymous = %d %s", rec.Code, rec.Body.String())
	}
}
