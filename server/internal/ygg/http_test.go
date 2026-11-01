package ygg

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// ---- in-memory store for contract tests ----

type memStore struct {
	users   map[string]User // by identifier
	byID    map[int64]User
	players map[string]Player // by name
	byUUID  map[string]Player
	tokens  map[string]Token
	joins   []JoinSession
}

func newMemStore() *memStore {
	return &memStore{
		users: map[string]User{}, byID: map[int64]User{},
		players: map[string]Player{}, byUUID: map[string]Player{},
		tokens: map[string]Token{},
	}
}

func (m *memStore) UserByID(_ context.Context, id int64) (User, error) {
	u, ok := m.byID[id]
	if !ok {
		return User{}, errors.New("no user")
	}
	return u, nil
}

func (m *memStore) UserByIdentifier(_ context.Context, ident string) (User, error) {
	u, ok := m.users[ident]
	if !ok {
		return User{}, errors.New("no user")
	}
	return u, nil
}

func (m *memStore) VerifyPassword(_ context.Context, userID int64, password string) (bool, error) {
	return password == "pw-ok", nil
}

func (m *memStore) PlayersByUID(_ context.Context, uid int64) ([]Player, error) {
	var out []Player
	for _, p := range m.players {
		if p.UID == uid {
			out = append(out, p)
		}
	}
	return out, nil
}

func (m *memStore) PlayerByUUID(_ context.Context, uuid string) (Player, error) {
	p, ok := m.byUUID[uuid]
	if !ok {
		return Player{}, errors.New("no player")
	}
	return p, nil
}

func (m *memStore) PlayerByName(_ context.Context, name string) (Player, error) {
	p, ok := m.players[name]
	if !ok {
		return Player{}, errors.New("no player")
	}
	return p, nil
}

func (m *memStore) SaveToken(_ context.Context, t Token) error {
	m.tokens[t.AccessToken] = t
	return nil
}

func (m *memStore) TokenByAccess(_ context.Context, access string) (Token, error) {
	t, ok := m.tokens[access]
	if !ok {
		return Token{}, errors.New("no token")
	}
	return t, nil
}

func (m *memStore) InvalidateToken(_ context.Context, access string) error {
	if t, ok := m.tokens[access]; ok {
		t.Invalidated = true
		m.tokens[access] = t
	}
	return nil
}

func (m *memStore) InvalidateUserTokens(_ context.Context, uid int64) error {
	for k, t := range m.tokens {
		if t.UserID == uid {
			t.Invalidated = true
			m.tokens[k] = t
		}
	}
	return nil
}

func (m *memStore) SaveJoin(_ context.Context, j JoinSession) error {
	m.joins = append(m.joins, j)
	return nil
}

func (m *memStore) JoinByTokenServer(_ context.Context, access, serverID string) (JoinSession, error) {
	for i := len(m.joins) - 1; i >= 0; i-- {
		j := m.joins[i]
		if j.AccessToken == access && j.ServerID == serverID {
			return j, nil
		}
	}
	return JoinSession{}, errors.New("no join")
}

func (m *memStore) FindLiveJoins(_ context.Context, uuid string, now time.Time) ([]JoinSession, error) {
	var out []JoinSession
	for _, j := range m.joins {
		if j.UUID == uuid && now.Before(j.ExpiresAt) {
			out = append(out, j)
		}
	}
	return out, nil
}

// ---- fixtures ----

const testUUID = "069a79f444e94726a5befca90e38aaf5"

func newTestAPI(t *testing.T) (*API, *memStore) {
	t.Helper()
	store := newMemStore()
	u := User{ID: 1, Nickname: "steve", EmailVerified: true, CanJoinServer: true}
	store.byID[1] = u
	store.users["steve"] = u
	store.users["steve@example.com"] = u
	p := Player{ID: 10, UID: 1, Name: "Steve", UUID: testUUID,
		SkinURL:   "https://skin.example.com/textures/" + strings.Repeat("ab", 32),
		SkinModel: "slim", CapeURL: ""}
	store.players["Steve"] = p
	store.byUUID[testUUID] = p

	signer, err := NewLocalSigner(t.TempDir() + "/key.pem")
	if err != nil {
		panic(err)
	}
	svc := NewService(store, signer)
	api := NewAPI(svc, MetadataConfig{
		ServerName:  "示例皮肤站",
		Version:     "test",
		Homepage:    "https://skin.example.com",
		Register:    "https://skin.example.com/auth/register",
		SkinDomains: []string{"skin.example.com"},
	})
	return api, store
}

func do(t *testing.T, h http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// ---- contract tests ----

func TestMetadataContract(t *testing.T) {
	api, _ := newTestAPI(t)
	rec := do(t, api.Handler(), "GET", "/api/yggdrasil/", "", nil)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	if got := rec.Header().Get("X-Authlib-Injector-API-Location"); !strings.Contains(got, "/api/yggdrasil") {
		t.Fatalf("ALI header = %q", got)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}
	meta := m["meta"].(map[string]interface{})
	if meta["serverName"] != "示例皮肤站" {
		t.Fatalf("serverName = %v", meta["serverName"])
	}
	if meta["implementationName"] != "SkinNexus" {
		t.Fatalf("implementationName = %v", meta["implementationName"])
	}
	if _, ok := meta["feature.non_email_login"]; !ok {
		t.Fatal("missing feature.non_email_login")
	}
	pem, _ := m["signaturePublickey"].(string)
	if !strings.HasPrefix(pem, "-----BEGIN PUBLIC KEY-----\n") || !strings.HasSuffix(pem, "-----END PUBLIC KEY-----\n") {
		t.Fatalf("PEM malformed:\n%q", pem)
	}
	for _, line := range strings.Split(strings.TrimSuffix(pem, "\n"), "\n") {
		if !strings.HasPrefix(line, "-----") && len(line) > 64 {
			t.Fatalf("PEM line >64 chars: %q", line)
		}
	}
}

func TestErrorBodyShape(t *testing.T) {
	api, _ := newTestAPI(t)
	rec := do(t, api.Handler(), "POST", "/api/yggdrasil/authserver/authenticate",
		`{"username":"steve","password":"wrong"}`, nil)
	if rec.Code != 403 {
		t.Fatalf("status %d", rec.Code)
	}
	var e map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatal(err)
	}
	if e["error"] != "ForbiddenOperationException" || e["errorMessage"] == "" {
		t.Fatalf("error body = %v", e)
	}
}

func TestNonJSONContentTypeGets200Error(t *testing.T) {
	api, _ := newTestAPI(t)
	req := httptest.NewRequest("POST", "/api/yggdrasil/authserver/authenticate", bytes.NewBufferString("x"))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	api.Handler().ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("legacy behavior: non-JSON must get 200+error, got %d", rec.Code)
	}
	var e map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &e)
	if e["error"] == "" {
		t.Fatal("missing error body")
	}
}

func Test204Semantics(t *testing.T) {
	api, _ := newTestAPI(t)
	h := api.Handler()
	cases := []struct{ method, path, body string }{
		{"POST", "/api/yggdrasil/authserver/invalidate", `{"accessToken":"nope"}`},       // always 204
		{"POST", "/api/yggdrasil/authserver/signout", `{"username":"x","password":"y"}`}, // always 204
		{"GET", "/api/yggdrasil/sessionserver/session/minecraft/hasJoined?username=Ghost&serverId=x", ""},
		{"GET", "/api/yggdrasil/sessionserver/session/minecraft/profile/" + strings.Repeat("0", 32), ""},
		{"GET", "/api/yggdrasil/api/users/profiles/minecraft/Notch", ""}, // live-capture parity
	}
	for _, c := range cases {
		rec := do(t, h, c.method, c.path, c.body, nil)
		if rec.Code != 204 {
			t.Errorf("%s %s: want 204 got %d", c.method, c.path, rec.Code)
		}
		if rec.Body.Len() != 0 {
			t.Errorf("%s %s: 204 must have empty body", c.method, c.path)
		}
	}
}

func TestAuthenticateAndTexturesShape(t *testing.T) {
	api, _ := newTestAPI(t)
	rec := do(t, api.Handler(), "POST", "/api/yggdrasil/authserver/authenticate",
		`{"username":"steve","password":"pw-ok","clientToken":"ct1","requestUser":false}`, nil)
	if rec.Code != 200 {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		AccessToken       string `json:"accessToken"`
		ClientToken       string `json:"clientToken"`
		AvailableProfiles []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"availableProfiles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.AccessToken == "" || resp.ClientToken != "ct1" || len(resp.AvailableProfiles) != 1 {
		t.Fatalf("resp = %s", rec.Body.String())
	}

	// hasJoined must produce a signed textures property.
	rec2 := do(t, api.Handler(), "POST", "/api/yggdrasil/sessionserver/session/minecraft/join",
		`{"accessToken":"`+resp.AccessToken+`","selectedProfile":{"id":"`+testUUID+`"},"serverId":"abc"}`, nil)
	if rec2.Code != 204 {
		t.Fatalf("join = %d", rec2.Code)
	}
	rec3 := do(t, api.Handler(), "GET",
		"/api/yggdrasil/sessionserver/session/minecraft/hasJoined?username=Steve&serverId=abc", "", nil)
	if rec3.Code != 200 {
		t.Fatalf("hasJoined = %d", rec3.Code)
	}
	assertTexturesSigned(t, rec3.Body.Bytes(), true)

	// profile without unsigned=false: no signature.
	rec4 := do(t, api.Handler(), "GET", "/api/yggdrasil/sessionserver/session/minecraft/profile/"+testUUID, "", nil)
	assertTexturesSigned(t, rec4.Body.Bytes(), false)
	// profile with unsigned=false: signature present.
	rec5 := do(t, api.Handler(), "GET", "/api/yggdrasil/sessionserver/session/minecraft/profile/"+testUUID+"?unsigned=false", "", nil)
	assertTexturesSigned(t, rec5.Body.Bytes(), true)
}

func assertTexturesSigned(t *testing.T, body []byte, wantSig bool) {
	t.Helper()
	var p struct {
		Properties []Property `json:"properties"`
	}
	if err := json.Unmarshal(body, &p); err != nil {
		t.Fatal(err)
	}
	var tex *Property
	for i := range p.Properties {
		if p.Properties[i].Name == "textures" {
			tex = &p.Properties[i]
		}
	}
	if tex == nil {
		t.Fatal("no textures property")
	}
	if wantSig && tex.Signature == "" {
		t.Fatal("textures signature missing")
	}
	if !wantSig && tex.Signature != "" {
		t.Fatal("unexpected textures signature")
	}
	raw, err := base64.StdEncoding.DecodeString(tex.Value)
	if err != nil {
		t.Fatal(err)
	}
	// Empty-textures zero-value rule: must be an object, never null/array.
	if bytes.Contains(raw, []byte(`"textures":null`)) || bytes.Contains(raw, []byte(`"textures":[]`)) {
		t.Fatalf("textures collapsed: %s", raw)
	}
	if !bytes.Contains(raw, []byte(`"textures":{}`)) && !bytes.Contains(raw, []byte(`"textures":{"SKIN"`)) {
		t.Fatalf("unexpected textures JSON: %s", raw)
	}
}

func TestEmptyTexturesObject(t *testing.T) {
	props, err := AssembleTextures(testUUID, "Steve", TextureSet{}, time.Now(), false, nil)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(props[0].Value)
	if !bytes.Contains(raw, []byte(`"textures":{}`)) {
		t.Fatalf("empty textures must be {}: %s", raw)
	}
}

func TestRefreshInvalidatesOldToken(t *testing.T) {
	api, _ := newTestAPI(t)
	h := api.Handler()
	rec := do(t, h, "POST", "/api/yggdrasil/authserver/authenticate",
		`{"username":"steve","password":"pw-ok","clientToken":"ct1"}`, nil)
	var resp struct {
		AccessToken string `json:"accessToken"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)

	rec2 := do(t, h, "POST", "/api/yggdrasil/authserver/refresh",
		`{"accessToken":"`+resp.AccessToken+`","clientToken":"ct1"}`, nil)
	if rec2.Code != 200 {
		t.Fatalf("refresh = %d", rec2.Code)
	}
	// Old token must now fail validate.
	rec3 := do(t, h, "POST", "/api/yggdrasil/authserver/validate",
		`{"accessToken":"`+resp.AccessToken+`","clientToken":"ct1"}`, nil)
	if rec3.Code != 403 {
		t.Fatalf("old token after refresh: want 403 got %d", rec3.Code)
	}
}
