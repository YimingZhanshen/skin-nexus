package oidc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"skin-nexus/server/internal/ygg"
)

// ---- in-memory Backend (contract double for store.Mysql) ----

type memBackend struct {
	mu       sync.Mutex
	clients  map[string]ClientRecord
	authReqs map[string]AuthRequestRecord
	tokens   map[string]AccessTokenRecord
	refresh  map[string]RefreshTokenRecord
	devices  map[string]DeviceAuthRecord
	byUser   map[string]string // user code -> device code
}

func newMemBackend() *memBackend {
	return &memBackend{
		clients:  map[string]ClientRecord{},
		authReqs: map[string]AuthRequestRecord{},
		tokens:   map[string]AccessTokenRecord{},
		refresh:  map[string]RefreshTokenRecord{},
		devices:  map[string]DeviceAuthRecord{},
		byUser:   map[string]string{},
	}
}

func (m *memBackend) GetClient(_ context.Context, id string) (ClientRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	c, ok := m.clients[id]
	if !ok {
		return ClientRecord{}, ErrNotFound
	}
	return c, nil
}

func (m *memBackend) ListClients(_ context.Context) ([]ClientRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]ClientRecord, 0, len(m.clients))
	for _, c := range m.clients {
		out = append(out, c)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (m *memBackend) DeleteClient(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.clients, id)
	return nil
}

func (m *memBackend) EnsureOidcClient(_ context.Context, rec ClientRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clients[rec.ID] = rec
	return nil
}

func (m *memBackend) SaveAuthRequest(_ context.Context, rec AuthRequestRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.authReqs[rec.ID] = rec
	return nil
}

func (m *memBackend) GetAuthRequest(_ context.Context, id string) (AuthRequestRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.authReqs[id]
	if !ok {
		return AuthRequestRecord{}, ErrNotFound
	}
	return r, nil
}

func (m *memBackend) GetAuthRequestByCode(_ context.Context, code string) (AuthRequestRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, r := range m.authReqs {
		if r.Code == code && code != "" {
			return r, nil
		}
	}
	return AuthRequestRecord{}, ErrNotFound
}

func (m *memBackend) DeleteAuthRequest(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.authReqs, id)
	return nil
}

func (m *memBackend) SaveAccessToken(_ context.Context, rec AccessTokenRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.tokens[rec.JTI] = rec
	return nil
}

func (m *memBackend) GetAccessToken(_ context.Context, jti string) (AccessTokenRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.tokens[jti]
	if !ok {
		return AccessTokenRecord{}, ErrNotFound
	}
	return r, nil
}

func (m *memBackend) RevokeAccessToken(_ context.Context, jti string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.tokens[jti]; ok {
		r.RevokedAt = time.Now()
		m.tokens[jti] = r
	}
	return nil
}

func (m *memBackend) RevokeUserAccessTokens(_ context.Context, uid int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, r := range m.tokens {
		if r.UserID == uid {
			r.RevokedAt = time.Now()
			m.tokens[k] = r
		}
	}
	return nil
}

func (m *memBackend) ListUserAccessTokens(_ context.Context, uid int64) ([]AccessTokenRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AccessTokenRecord, 0)
	for _, r := range m.tokens {
		if r.UserID == uid && r.RevokedAt.IsZero() && r.ExpiresAt.After(time.Now()) {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (m *memBackend) SaveRefreshToken(_ context.Context, rec RefreshTokenRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.refresh[rec.Token] = rec
	return nil
}

func (m *memBackend) GetRefreshToken(_ context.Context, token string) (RefreshTokenRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.refresh[token]
	if !ok {
		return RefreshTokenRecord{}, ErrNotFound
	}
	return r, nil
}

func (m *memBackend) RotateRefreshToken(_ context.Context, old, newRec RefreshTokenRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.refresh[old.Token]; ok {
		r.Revoked = true
		m.refresh[old.Token] = r
	}
	m.refresh[newRec.Token] = newRec
	return nil
}

func (m *memBackend) RevokeRefreshToken(_ context.Context, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if r, ok := m.refresh[token]; ok {
		r.Revoked = true
		m.refresh[token] = r
	}
	return nil
}

func (m *memBackend) RevokeUserRefreshTokens(_ context.Context, uid int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, r := range m.refresh {
		if r.UserID == uid {
			r.Revoked = true
			m.refresh[k] = r
		}
	}
	return nil
}

func (m *memBackend) RevokeClientRefreshTokens(_ context.Context, uid int64, clientID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for k, r := range m.refresh {
		if r.UserID == uid && r.ClientID == clientID {
			r.Revoked = true
			m.refresh[k] = r
		}
	}
	return nil
}

func (m *memBackend) SaveDeviceAuth(_ context.Context, rec DeviceAuthRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, dup := m.byUser[rec.UserCode]; dup {
		return ErrDuplicateUserCode
	}
	m.devices[rec.DeviceCode] = rec
	m.byUser[rec.UserCode] = rec.DeviceCode
	return nil
}

func (m *memBackend) GetDeviceAuthByDeviceCode(_ context.Context, code string) (DeviceAuthRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.devices[code]
	if !ok {
		return DeviceAuthRecord{}, ErrNotFound
	}
	return r, nil
}

func (m *memBackend) GetDeviceAuthByUserCode(_ context.Context, code string) (DeviceAuthRecord, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	dc, ok := m.byUser[code]
	if !ok {
		return DeviceAuthRecord{}, ErrNotFound
	}
	return m.devices[dc], nil
}

func (m *memBackend) UpdateDeviceAuth(_ context.Context, rec DeviceAuthRecord) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.devices[rec.DeviceCode]
	if !ok || cur.Done || cur.Denied {
		return ErrNotFound // pending->decided guard, mirrors the SQL store
	}
	m.devices[rec.DeviceCode] = rec
	return nil
}

func (m *memBackend) ConsumeDeviceAuth(_ context.Context, code string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	rec, ok := m.devices[code]
	if !ok || !rec.Done || rec.Denied {
		return ErrNotFound
	}
	delete(m.devices, code)
	for k, v := range m.byUser {
		if v == code {
			delete(m.byUser, k)
			break
		}
	}
	return nil
}

func (m *memBackend) Health(context.Context) error { return nil }

// ---- fixtures ----

func testKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func testProvider(t *testing.T) (*Provider, *memBackend) {
	t.Helper()
	b := newMemBackend()
	p, err := New(b, ProfileSource{
		Players: func(context.Context, int64) ([]PlayerBrief, error) {
			return []PlayerBrief{
				{ID: 7, UUID: "aabbccdd00112233445566778899aaab", Name: "Steve"},
				{ID: 8, UUID: "bbccdd00112233445566778899aabbcc", Name: "Alex"},
			}, nil
		},
		User: func(context.Context, int64) (UserBrief, error) {
			return UserBrief{ID: 42, Nickname: "ymzs", Email: "u@example.com", EmailVerified: true}, nil
		},
	}, Config{
		Issuer:         "https://skin.test",
		Key:            testKey(t),
		SharedClientID: "2",
		LoginURL:       "https://skin.test/login",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SeedSharedClient(context.Background()); err != nil {
		t.Fatal(err)
	}
	return p, b
}

func form(t *testing.T, h http.Handler, method, path string, vals url.Values) *httptest.ResponseRecorder {
	t.Helper()
	var body *strings.Reader
	if vals == nil {
		body = strings.NewReader("")
	} else {
		body = strings.NewReader(vals.Encode())
	}
	req := httptest.NewRequest(method, path, body)
	if vals != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decodeJWTClaims(t *testing.T, jwt string) map[string]any {
	t.Helper()
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("not a JWT: %q", jwt)
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payload, &claims); err != nil {
		t.Fatal(err)
	}
	return claims
}

// verifyJWTSignature checks RS256 against the provider key independently.
func verifyJWTSignature(t *testing.T, key *rsa.PublicKey, jwt string) {
	t.Helper()
	parts := strings.Split(jwt, ".")
	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		t.Fatal(err)
	}
	h := crypto.SHA256.New()
	h.Write([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(key, crypto.SHA256, h.Sum(nil), sig); err != nil {
		t.Fatalf("bad JWT signature: %v", err)
	}
}

// ---- contract tests (OIDC client contract, docs/compat-matrix.md) ----

func TestDiscoveryContract(t *testing.T) {
	p, _ := testProvider(t)
	rec := form(t, p.Wrap(http.NotFoundHandler()), "GET", "/.well-known/openid-configuration", nil)
	if rec.Code != 200 {
		t.Fatalf("status %d", rec.Code)
	}
	var doc map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	// Hard contract items (both launchers).
	if doc["shared_client_id"] != "2" {
		t.Errorf("shared_client_id = %v", doc["shared_client_id"])
	}
	for _, f := range []string{"issuer", "authorization_endpoint", "token_endpoint",
		"device_authorization_endpoint", "userinfo_endpoint", "jwks_uri",
		"scopes_supported", "subject_types_supported", "id_token_signing_alg_values_supported"} {
		if _, ok := doc[f]; !ok {
			t.Errorf("missing discovery field %q", f)
		}
	}
	if doc["issuer"] != "https://skin.test" {
		t.Errorf("issuer = %v", doc["issuer"])
	}
	scopes, _ := json.Marshal(doc["scopes_supported"])
	for _, want := range []string{"openid", "Yggdrasil.PlayerProfiles.Select", "Yggdrasil.Server.Join", "offline_access"} {
		if !strings.Contains(string(scopes), want) {
			t.Errorf("scopes_supported missing %s: %s", want, scopes)
		}
	}
	algs, _ := json.Marshal(doc["id_token_signing_alg_values_supported"])
	if string(algs) != `["RS256"]` {
		t.Errorf("id_token algs = %s (SJMCL only accepts RS256)", algs)
	}
	claims, _ := json.Marshal(doc["claims_supported"])
	if !strings.Contains(string(claims), "selectedProfile") {
		t.Errorf("claims_supported missing selectedProfile: %s", claims)
	}
}

func TestDeviceFlowEndToEnd(t *testing.T) {
	p, _ := testProvider(t)
	h := p.Wrap(http.NotFoundHandler())

	// 1. device authorization (public shared client, no secret).
	rec := form(t, h, "POST", "/device_authorization", url.Values{
		"client_id": {"2"},
		"scope":     {"openid offline_access Yggdrasil.PlayerProfiles.Select Yggdrasil.Server.Join"},
	})
	if rec.Code != 200 {
		t.Fatalf("device_authorization status %d: %s", rec.Code, rec.Body.String())
	}
	var da struct {
		DeviceCode              string `json:"device_code"`
		UserCode                string `json:"user_code"`
		VerificationURI         string `json:"verification_uri"`
		VerificationURIComplete string `json:"verification_uri_complete"`
		ExpiresIn               int    `json:"expires_in"`
		Interval                int    `json:"interval"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &da); err != nil {
		t.Fatal(err)
	}
	if da.DeviceCode == "" || da.UserCode == "" || da.VerificationURI == "" {
		t.Fatalf("device response missing fields: %s", rec.Body.String())
	}
	if !regexp.MustCompile(`^[A-Z]{4}-[A-Z]{4}$`).MatchString(da.UserCode) {
		t.Errorf("user_code shape = %q (want XXXX-XXXX)", da.UserCode)
	}
	if da.ExpiresIn != 600 {
		t.Errorf("expires_in = %d (want 600)", da.ExpiresIn)
	}
	if !strings.Contains(da.VerificationURIComplete, url.QueryEscape(da.UserCode)) {
		t.Errorf("verification_uri_complete %q missing user_code", da.VerificationURIComplete)
	}

	// 2. poll before approval -> 400 authorization_pending.
	poll := func() (int, map[string]any) {
		r := form(t, h, "POST", "/oauth/token", url.Values{
			"client_id":   {"2"},
			"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
			"device_code": {da.DeviceCode},
		})
		var body map[string]any
		_ = json.Unmarshal(r.Body.Bytes(), &body)
		return r.Code, body
	}
	if code, body := poll(); code != 400 || body["error"] != "authorization_pending" {
		t.Fatalf("pending poll = %d %v", code, body)
	}

	// 3. approve with profile 7 selected (device verification page action).
	if err := p.ApproveDevice(context.Background(), 42, da.UserCode, 7); err != nil {
		t.Fatal(err)
	}

	// 4. poll -> token response with the full launcher contract.
	code, tok := poll()
	if code != 200 {
		t.Fatalf("token poll = %d %v", code, tok)
	}
	for _, f := range []string{"access_token", "token_type", "expires_in", "refresh_token", "id_token"} {
		if _, ok := tok[f]; !ok {
			t.Fatalf("token response missing %q: %v", f, tok)
		}
	}
	if tok["token_type"] != "Bearer" {
		t.Errorf("token_type = %v", tok["token_type"])
	}
	access, _ := tok["access_token"].(string)
	refresh, _ := tok["refresh_token"].(string)
	idToken, _ := tok["id_token"].(string)

	// 5. id_token: RS256 signature, claims per contract.
	verifyJWTSignature(t, &p.cfg.Key.PublicKey, idToken)
	claims := decodeJWTClaims(t, idToken)
	if claims["iss"] != "https://skin.test" {
		t.Errorf("iss = %v", claims["iss"])
	}
	if claims["sub"] != "42" {
		t.Errorf("sub = %v (want \"42\")", claims["sub"])
	}
	aud, _ := json.Marshal(claims["aud"])
	if !strings.Contains(string(aud), "2") {
		t.Errorf("aud = %s (want shared client)", aud)
	}
	sel, _ := json.Marshal(claims["selectedProfile"])
	if !strings.Contains(string(sel), "aabbccdd00112233445566778899aaab") {
		t.Errorf("selectedProfile = %s (want player 7 uuid)", sel)
	}

	// 6. userinfo (dual path /userinfo and /yggc/userinfo).
	for _, path := range []string{"/userinfo", "/yggc/userinfo"} {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Authorization", "Bearer "+access)
		ur := httptest.NewRecorder()
		h.ServeHTTP(ur, req)
		if ur.Code != 200 {
			t.Fatalf("userinfo %s status %d: %s", path, ur.Code, ur.Body.String())
		}
		var ui map[string]any
		_ = json.Unmarshal(ur.Body.Bytes(), &ui)
		if ui["sub"] != "42" {
			t.Errorf("userinfo sub = %v", ui["sub"])
		}
		if _, ok := ui["selectedProfile"]; !ok {
			t.Errorf("userinfo missing selectedProfile: %v", ui)
		}
	}

	// 7. access token is a Yggdrasil token (dual-state) — resolver round-trip.
	resolver := NewYggTokenResolver(nil, "https://skin.test", &p.cfg.Key.PublicKey)
	_ = resolver
	yt, err := ResolveForTest(context.Background(), p, access)
	if err != nil {
		t.Fatalf("ygg resolver: %v", err)
	}
	if yt.UserID != 42 || yt.SelectedPID != 7 {
		t.Errorf("resolved token = %+v", yt)
	}

	// 8. refresh rotates: old refresh dies, new token pair arrives.
	r2 := form(t, h, "POST", "/oauth/token", url.Values{
		"client_id":     {"2"},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
	})
	if r2.Code != 200 {
		t.Fatalf("refresh = %d %s", r2.Code, r2.Body.String())
	}
	var tok2 map[string]any
	_ = json.Unmarshal(r2.Body.Bytes(), &tok2)
	if tok2["refresh_token"] == nil || tok2["id_token"] == nil {
		t.Fatalf("refresh response missing refresh_token/id_token: %v", tok2)
	}
	r3 := form(t, h, "POST", "/oauth/token", url.Values{
		"client_id":     {"2"},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
	})
	if r3.Code == 200 {
		t.Fatalf("reused refresh token must fail, got 200")
	}

	// 9. revocation: password-change hook kills the jti.
	if err := p.RevokeUserTokens(context.Background(), 42); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveForTest(context.Background(), p, access); err == nil {
		t.Fatalf("revoked jti must not resolve")
	}
}

func TestDeviceDenied(t *testing.T) {
	p, _ := testProvider(t)
	h := p.Wrap(http.NotFoundHandler())
	rec := form(t, h, "POST", "/device_authorization", url.Values{
		"client_id": {"2"}, "scope": {"openid Yggdrasil.Server.Join Yggdrasil.PlayerProfiles.Select"},
	})
	var da struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &da)
	if err := p.DenyDevice(context.Background(), da.UserCode); err != nil {
		t.Fatal(err)
	}
	r := form(t, h, "POST", "/oauth/token", url.Values{
		"client_id": {"2"}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {da.DeviceCode},
	})
	var body map[string]any
	_ = json.Unmarshal(r.Body.Bytes(), &body)
	if r.Code != 400 || body["error"] != "access_denied" {
		t.Fatalf("denied poll = %d %v", r.Code, body)
	}
}

func TestUserinfoUnauthorized(t *testing.T) {
	p, _ := testProvider(t)
	h := p.Wrap(http.NotFoundHandler())
	for _, authz := range []string{"", "Bearer not-a-jwt", "Bearer a.b.c"} {
		req := httptest.NewRequest("GET", "/userinfo", nil)
		if authz != "" {
			req.Header.Set("Authorization", authz)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != 401 {
			t.Fatalf("authz %q status %d", authz, rec.Code)
		}
		if got := rec.Header().Get("WWW-Authenticate"); got != `Bearer realm="openid"` {
			t.Fatalf("WWW-Authenticate = %q", got)
		}
	}
	// POST must be 405 (legacy: GET/HEAD only).
	rec := form(t, h, "POST", "/userinfo", url.Values{})
	if rec.Code != 405 {
		t.Fatalf("POST /userinfo = %d", rec.Code)
	}
}

func TestYggResolverGates(t *testing.T) {
	p, _ := testProvider(t)
	h := p.Wrap(http.NotFoundHandler())

	// Issue a token WITHOUT the join scope, then one with it.
	mk := func(scope string) string {
		rec := form(t, h, "POST", "/device_authorization", url.Values{"client_id": {"2"}, "scope": {scope}})
		var da struct {
			UserCode   string `json:"user_code"`
			DeviceCode string `json:"device_code"`
		}
		_ = json.Unmarshal(rec.Body.Bytes(), &da)
		_ = p.ApproveDevice(context.Background(), 42, da.UserCode, 7)
		r := form(t, h, "POST", "/oauth/token", url.Values{
			"client_id": {"2"}, "grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "device_code": {da.DeviceCode},
		})
		var body map[string]any
		_ = json.Unmarshal(r.Body.Bytes(), &body)
		at, _ := body["access_token"].(string)
		return at
	}

	resolver := NewYggTokenResolver(p.backend, "https://skin.test", &p.cfg.Key.PublicKey)

	noJoin := mk("openid offline_access Yggdrasil.PlayerProfiles.Select")
	if _, err := resolver(context.Background(), noJoin); err == nil {
		t.Fatalf("token without Server.Join scope must not resolve as Yggdrasil token")
	}

	good := mk("openid offline_access Yggdrasil.PlayerProfiles.Select Yggdrasil.Server.Join")
	if _, err := resolver(context.Background(), good); err != nil {
		t.Fatalf("join-scoped token must resolve: %v", err)
	}

	// Tampered signature.
	parts := strings.Split(good, ".")
	forged := parts[0] + "." + parts[1] + "." + strings.Repeat("A", len(parts[2]))
	if _, err := resolver(context.Background(), forged); err == nil {
		t.Fatalf("forged signature must fail")
	}

	// Non-JWT falls through as ErrNotJOSE (opaque fallback upstream).
	if _, err := resolver(context.Background(), "opaque-token-value"); err != ErrNotJOSE {
		t.Fatalf("opaque token err = %v (want ErrNotJOSE)", err)
	}
}

// ResolveForTest exercises the resolver path with the provider's backend.
func ResolveForTest(ctx context.Context, p *Provider, access string) (ygg.Token, error) {
	resolver := NewYggTokenResolver(p.backend, p.cfg.Issuer, &p.cfg.Key.PublicKey)
	return resolver(ctx, access)
}

// ---- authorized-apps management API (T1.2) ----

func TestTokensListAndRevoke(t *testing.T) {
	p, b := testProvider(t)
	p.SessionResolver = func(context.Context, string) (int64, error) { return 42, nil }
	h := p.Wrap(http.NotFoundHandler())

	// Seed one live token (uid 42, shared client) and one other-user token.
	live := AccessTokenRecord{
		JTI: "jti-live", UserID: 42, ClientID: "2",
		Scopes:    []string{"openid", "Yggdrasil.Server.Join"},
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}
	if err := b.SaveAccessToken(context.Background(), live); err != nil {
		t.Fatal(err)
	}
	if err := b.SaveAccessToken(context.Background(), AccessTokenRecord{
		JTI: "jti-other", UserID: 7, ClientID: "2",
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := b.SaveRefreshToken(context.Background(), RefreshTokenRecord{
		Token: "rt-live", UserID: 42, ClientID: "2",
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}

	// List: only the caller's live entry, shape per work card T1.2.
	req := httptest.NewRequest("GET", "/api/v1/oidc/tokens", nil)
	req.AddCookie(&http.Cookie{Name: "skin_session", Value: "s1"})
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("tokens list status %d: %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Tokens []struct {
			JTI      string   `json:"jti"`
			ClientID string   `json:"client_id"`
			Scopes   []string `json:"scopes"`
		} `json:"tokens"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Tokens) != 1 || list.Tokens[0].JTI != "jti-live" {
		t.Fatalf("tokens = %s", rec.Body.String())
	}

	// Revoke someone else's jti -> not-found (ownership gate).
	other := func(jti string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/v1/oidc/tokens/revoke", strings.NewReader(`{"jti":"`+jti+`"}`))
		r.Header.Set("Content-Type", "application/json")
		r.AddCookie(&http.Cookie{Name: "skin_session", Value: "s1"})
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := other("jti-other"); w.Code != 404 || !strings.Contains(w.Body.String(), `"code":"oidc.grant_not_found"`) {
		t.Fatalf("cross-user revoke = %d %s", w.Code, w.Body.String())
	}

	// Revoke own jti -> ok, ledger entry marked revoked, client refresh revoked.
	if w := other("jti-live"); !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("revoke = %s", w.Body.String())
	}
	revoked, err := b.GetAccessToken(context.Background(), "jti-live")
	if err != nil {
		t.Fatal(err)
	}
	if revoked.RevokedAt.IsZero() {
		t.Fatal("jti ledger entry not marked revoked")
	}
	rt, err := b.GetRefreshToken(context.Background(), "rt-live")
	if err != nil || !rt.Revoked {
		t.Fatalf("client refresh not revoked: %+v %v", rt, err)
	}

	// Revoke-all path.
	if err := b.SaveAccessToken(context.Background(), live); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "/api/v1/oidc/tokens/revoke", strings.NewReader(`{"all":true}`))
	r.Header.Set("Content-Type", "application/json")
	r.AddCookie(&http.Cookie{Name: "skin_session", Value: "s1"})
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), `"scope":"all"`) {
		t.Fatalf("revoke all = %s", w.Body.String())
	}

	// Unauthenticated -> 401.
	r2 := httptest.NewRequest("GET", "/api/v1/oidc/tokens", nil)
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, r2)
	if w2.Code != 401 {
		t.Fatalf("unauth tokens list = %d", w2.Code)
	}
}
