package serveraccess

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/metrics"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/ygg"
)

type verifyFixture struct {
	saFixture
	api      *ygg.API
	signer   *ygg.LocalSigner
	secret   string
	fallback int
	globalOn bool
}

func newVerifyFixture(t *testing.T) *verifyFixture {
	t.Helper()
	base := newSAFixture(t)
	signer, err := ygg.NewLocalSigner(filepath.Join(t.TempDir(), "verify-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	api := ygg.NewAPI(ygg.NewService(base.st, signer), ygg.MetadataConfig{})
	f := &verifyFixture{saFixture: *base, api: api, signer: signer, secret: "test-only-verify-secret-0123456789abcdef", globalOn: true}
	if err := f.st.SetOption(context.Background(), "serveraccess_global_fallback", "1"); err != nil {
		t.Fatal(err)
	}
	api.SetFallback(&ygg.Fallback{BaseURL: "https://official.example", Enabled: func(context.Context) bool { return f.globalOn }, Client: &http.Client{Transport: verifyFallback(func(*http.Request) (*http.Response, error) {
		f.fallback++
		return verifyFallbackResponse(`{"id":"0123456789abcdef0123456789abcdef","name":"Unknown"}`), nil
	})}})
	f.stack = NewStack(Config{Enabled: true, Store: f.st, Now: func() time.Time { return f.now }, Verifier: api})
	f.h = f.stack.Gate(f.stack.Handler())
	return f
}

type verifyFallback func(*http.Request) (*http.Response, error)

func (f verifyFallback) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func verifyFallbackResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}

func (f *verifyFixture) credential(t *testing.T, mode string) int64 {
	t.Helper()
	sum := sha256.Sum256([]byte(f.secret))
	id, err := f.st.CreateSAServerCredential(context.Background(), f.tenant, f.server, store.SACredential{
		Mode: mode, SecretHash: hex.EncodeToString(sum[:]), Scopes: `["verify:join"]`, Status: "active",
		ExpiresAt: sql.NullTime{Time: f.now.Add(time.Hour), Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func (f *verifyFixture) playerAndJoin(t *testing.T) {
	t.Helper()
	_, uuid, err := f.st.CreatePlayer(context.Background(), f.owner, "Alice")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.SaveJoin(context.Background(), ygg.JoinSession{AccessToken: "opaque", UUID: uuid, ServerID: "server-id", ServerIP: "203.0.113.8", CreatedAt: f.now, ExpiresAt: f.now.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
}

func (f *verifyFixture) verify(t *testing.T, mode, name, sid, ip string) *httptest.ResponseRecorder {
	t.Helper()
	path := "/v/" + f.secret + "/sessionserver/session/minecraft/hasJoined"
	if mode == "header" {
		path = "/api/serveraccess/v1/servers/srv_one/hasJoined"
	}
	r := httptest.NewRequest(http.MethodGet, path+"?username="+name+"&serverId="+sid+"&ip="+ip, nil)
	if mode == "header" {
		r.Header.Set("Authorization", "Bearer "+f.secret)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	return rec
}

func TestServerAccessURLSecretReturnsSignedProfileAndDeduplicatesEvent(t *testing.T) {
	f := newVerifyFixture(t)
	f.credential(t, "url_secret")
	f.playerAndJoin(t)
	for i := 0; i < 2; i++ {
		rec := f.verify(t, "url_secret", "Alice", "server-id", "203.0.113.8")
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"name":"Alice"`) || !strings.Contains(rec.Body.String(), `"signature"`) {
			t.Fatalf("verification = %d %s", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("security headers = %v", rec.Header())
		}
	}
	events, err := f.st.ListSAJoinEvents(context.Background(), f.tenant, f.server, f.now.Add(-time.Minute), f.now.Add(time.Minute), 20)
	if err != nil || len(events) != 1 {
		t.Fatalf("replay events = %+v, %v", events, err)
	}
	event := events[0]
	if event.UID.Int64 != f.owner || event.PlayerName != "Alice" || event.UUID == "" || !event.JoinSessionID.Valid || event.Outcome != "allow" || event.ReasonCode != "local_session" || event.SourceMode != "url_secret" || event.TrustLevel() != "possessed_credential" || event.DurationMS < 0 || event.RequestID == "" {
		t.Fatalf("event fields = %+v", event)
	}
}

func TestServerAccessHeaderCredentialAndWrongServerFailClosed(t *testing.T) {
	f := newVerifyFixture(t)
	f.credential(t, "header")
	f.playerAndJoin(t)
	if rec := f.verify(t, "header", "Alice", "server-id", "203.0.113.8"); rec.Code != http.StatusOK {
		t.Fatalf("header verification = %d %s", rec.Code, rec.Body.String())
	}
	for _, publicID := range []string{"missing", "srv_two"} {
		r := httptest.NewRequest(http.MethodGet, "/api/serveraccess/v1/servers/"+publicID+"/hasJoined?username=Alice&serverId=server-id", nil)
		r.Header.Set("Authorization", "Bearer "+f.secret)
		rec := httptest.NewRecorder()
		f.h.ServeHTTP(rec, r)
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), f.secret) {
			t.Fatalf("wrong public server %s = %d %s", publicID, rec.Code, rec.Body.String())
		}
	}
	if rec := f.verify(t, "url_secret", "Alice", "server-id", ""); rec.Code != http.StatusNotFound {
		t.Fatalf("header credential on URL route = %d, want 404", rec.Code)
	}
}

func TestServerAccessPlatformBanPrecedesTenantBanAndWhitelist(t *testing.T) {
	f := newVerifyFixture(t)
	f.credential(t, "url_secret")
	f.playerAndJoin(t)
	enabled := true
	if err := f.st.UpdateSAServerOwnerPolicy(context.Background(), f.tenant, f.server, &enabled, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AddSAServerBan(context.Background(), f.tenant, f.server, f.owner, "tenant", sql.NullTime{}, f.owner); err != nil {
		t.Fatal(err)
	}
	if err := f.st.SetUserPermission(context.Background(), f.owner, -1); err != nil {
		t.Fatal(err)
	}
	rec := f.verify(t, "url_secret", "Alice", "server-id", "203.0.113.8")
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 || f.fallback != 0 {
		t.Fatalf("platform ban = %d %s fallback=%d", rec.Code, rec.Body.String(), f.fallback)
	}
	events, err := f.st.ListSAJoinEvents(context.Background(), f.tenant, f.server, f.now.Add(-time.Minute), f.now.Add(time.Minute), 20)
	if err != nil || len(events) != 1 || events[0].ReasonCode != "platform_banned" || !events[0].UID.Valid || events[0].UID.Int64 != f.owner || events[0].UUID == "" {
		t.Fatalf("platform ban event = %+v, %v", events, err)
	}
}

func TestServerAccessTenantBanAndKnownWhitelistDenyPrecedeFallback(t *testing.T) {
	for _, tc := range []struct {
		name, reason string
		ban          bool
	}{
		{name: "tenant ban", reason: "tenant_banned", ban: true},
		{name: "known player not whitelisted", reason: "not_whitelisted"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newVerifyFixture(t)
			f.credential(t, "url_secret")
			f.playerAndJoin(t)
			enabled := true
			if err := f.st.UpdateSAServerOwnerPolicy(context.Background(), f.tenant, f.server, &enabled, nil); err != nil {
				t.Fatal(err)
			}
			if tc.ban {
				if err := f.st.AddSAServerBan(context.Background(), f.tenant, f.server, f.owner, "tenant", sql.NullTime{}, f.owner); err != nil {
					t.Fatal(err)
				}
			}
			rec := f.verify(t, "url_secret", "Alice", "server-id", "203.0.113.8")
			if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 || f.fallback != 0 {
				t.Fatalf("deny = %d %s fallback=%d", rec.Code, rec.Body.String(), f.fallback)
			}
			events, err := f.st.ListSAJoinEvents(context.Background(), f.tenant, f.server, f.now.Add(-time.Minute), f.now.Add(time.Minute), 20)
			if err != nil || len(events) != 1 || events[0].ReasonCode != tc.reason || !events[0].UID.Valid || events[0].UID.Int64 != f.owner {
				t.Fatalf("deny event = %+v, %v", events, err)
			}
		})
	}
}

func TestServerAccessWhitelistUnknownPlayerDeniesFallback(t *testing.T) {
	f := newVerifyFixture(t)
	f.credential(t, "url_secret")
	enabled := true
	if err := f.st.UpdateSAServerOwnerPolicy(context.Background(), f.tenant, f.server, &enabled, nil); err != nil {
		t.Fatal(err)
	}
	rec := f.verify(t, "url_secret", "Unknown", "server-id", "")
	if rec.Code != http.StatusNoContent || f.fallback != 0 {
		t.Fatalf("unknown whitelist = %d %s fallback=%d", rec.Code, rec.Body.String(), f.fallback)
	}
	events, err := f.st.ListSAJoinEvents(context.Background(), f.tenant, f.server, f.now.Add(-time.Minute), f.now.Add(time.Minute), 20)
	if err != nil || len(events) != 1 || events[0].ReasonCode != "whitelist_unknown_player" || events[0].UID.Valid || events[0].UUID != "" {
		t.Fatalf("unknown event = %+v, %v", events, err)
	}
}

func TestServerAccessWhitelistKnownPlayerMissDeniesFallback(t *testing.T) {
	f := newVerifyFixture(t)
	f.credential(t, "url_secret")
	if _, _, err := f.st.CreatePlayer(context.Background(), f.owner, "Alice"); err != nil {
		t.Fatal(err)
	}
	enabled := true
	if err := f.st.UpdateSAServerOwnerPolicy(context.Background(), f.tenant, f.server, &enabled, nil); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AddSAServerWhitelist(context.Background(), f.tenant, f.server, f.owner, "allowed", f.owner); err != nil {
		t.Fatal(err)
	}
	rec := f.verify(t, "url_secret", "Alice", "server-id", "")
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 || f.fallback != 0 {
		t.Fatalf("known whitelist miss = %d %s fallback=%d", rec.Code, rec.Body.String(), f.fallback)
	}
	events, err := f.st.ListSAJoinEvents(context.Background(), f.tenant, f.server, f.now.Add(-time.Minute), f.now.Add(time.Minute), 20)
	if err != nil || len(events) != 1 || events[0].ReasonCode != "session_not_found" || events[0].UID.Int64 != f.owner {
		t.Fatalf("known whitelist miss event = %+v, %v", events, err)
	}
}

func TestServerAccessLocalHitDoesNotReadGlobalFallbackOption(t *testing.T) {
	f := newVerifyFixture(t)
	f.credential(t, "url_secret")
	f.playerAndJoin(t)
	if _, err := f.st.DB().Exec(`DROP TABLE options`); err != nil {
		t.Fatal(err)
	}
	rec := f.verify(t, "url_secret", "Alice", "server-id", "203.0.113.8")
	if rec.Code != http.StatusOK || f.fallback != 0 {
		t.Fatalf("local hit with fallback option fault = %d %s fallback=%d", rec.Code, rec.Body.String(), f.fallback)
	}
	events, err := f.st.ListSAJoinEvents(context.Background(), f.tenant, f.server, f.now.Add(-time.Minute), f.now.Add(time.Minute), 20)
	if err != nil || len(events) != 1 || events[0].ReasonCode != "local_session" {
		t.Fatalf("local hit event = %+v, %v", events, err)
	}
}

func TestServerAccessFallbackEventUsesOfficialIdentityWithoutLocalUID(t *testing.T) {
	const officialUUID = "fedcba9876543210fedcba9876543210"
	for _, tc := range []struct {
		name, username, officialName string
		localPlayer                  bool
	}{
		{name: "unknown player", username: "Unknown", officialName: "OfficialUnknown"},
		{name: "known local miss", username: "Alice", officialName: "OfficialAlice", localPlayer: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newVerifyFixture(t)
			f.credential(t, "url_secret")
			if tc.localPlayer {
				if _, _, err := f.st.CreatePlayer(context.Background(), f.owner, "Alice"); err != nil {
					t.Fatal(err)
				}
			}
			body := `{"id":"` + officialUUID + `","name":"` + tc.officialName + `"}`
			f.api.SetFallback(&ygg.Fallback{BaseURL: "https://official.example", Enabled: func(context.Context) bool { return true }, Client: &http.Client{Transport: verifyFallback(func(*http.Request) (*http.Response, error) {
				return verifyFallbackResponse(body), nil
			})}})
			rec := f.verify(t, "url_secret", tc.username, "server-id", "")
			if rec.Code != http.StatusOK || rec.Body.String() != body {
				t.Fatalf("official response = %d %s", rec.Code, rec.Body.String())
			}
			events, err := f.st.ListSAJoinEvents(context.Background(), f.tenant, f.server, f.now.Add(-time.Minute), f.now.Add(time.Minute), 20)
			if err != nil || len(events) != 1 {
				t.Fatalf("fallback events = %+v, %v", events, err)
			}
			event := events[0]
			if event.UUID != officialUUID || event.PlayerName != tc.officialName || event.UID.Valid || event.ReasonCode != "official_fallback" {
				t.Fatalf("official fallback identity = %+v", event)
			}
		})
	}
}

func TestServerAccessFallbackRejectsMalformedOfficialIdentity(t *testing.T) {
	for _, body := range []string{
		`{"id":"not-a-uuid","name":"Official"}`,
		`{"id":{"unexpected":"object"},"name":"Official"}`,
	} {
		f := newVerifyFixture(t)
		f.credential(t, "url_secret")
		f.api.SetFallback(&ygg.Fallback{BaseURL: "https://official.example", Enabled: func(context.Context) bool { return true }, Client: &http.Client{Transport: verifyFallback(func(*http.Request) (*http.Response, error) {
			return verifyFallbackResponse(body), nil
		})}})
		rec := f.verify(t, "url_secret", "Unknown", "server-id", "")
		if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
			t.Fatalf("malformed official identity = %d %s", rec.Code, rec.Body.String())
		}
		events, err := f.st.ListSAJoinEvents(context.Background(), f.tenant, f.server, f.now.Add(-time.Minute), f.now.Add(time.Minute), 20)
		if err != nil || len(events) != 1 || events[0].ReasonCode != "session_not_found" || events[0].UUID != "" || events[0].UID.Valid {
			t.Fatalf("malformed official identity event = %+v, %v", events, err)
		}
	}
}

func TestServerAccessFallbackRequiresTrueMissAndBothSwitches(t *testing.T) {
	for _, tc := range []struct {
		name       string
		server     bool
		platform   bool
		yggGlobal  bool
		unknown    bool
		wantStatus int
		wantCalls  int
		wantReason string
	}{
		{name: "true miss enabled", server: true, platform: true, yggGlobal: true, unknown: true, wantStatus: 200, wantCalls: 1, wantReason: "official_fallback"},
		{name: "server switch off", platform: true, yggGlobal: true, unknown: true, wantStatus: 204, wantReason: "session_not_found"},
		{name: "platform switch off", server: true, yggGlobal: true, unknown: true, wantStatus: 204, wantReason: "session_not_found"},
		{name: "ygg global switch off", server: true, platform: true, unknown: true, wantStatus: 204, wantReason: "session_not_found"},
		{name: "local hit never falls back", server: true, platform: true, yggGlobal: true, wantStatus: 200, wantReason: "local_session"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newVerifyFixture(t)
			f.credential(t, "url_secret")
			if err := f.st.UpdateSAServerOwnerPolicy(context.Background(), f.tenant, f.server, nil, &tc.server); err != nil {
				t.Fatal(err)
			}
			f.globalOn = tc.yggGlobal
			platformValue := "0"
			if tc.platform {
				platformValue = "1"
			}
			if err := f.st.SetOption(context.Background(), "serveraccess_global_fallback", platformValue); err != nil {
				t.Fatal(err)
			}
			name := "Unknown"
			if !tc.unknown {
				f.playerAndJoin(t)
				name = "Alice"
			}
			rec := f.verify(t, "url_secret", name, "server-id", "203.0.113.8")
			if rec.Code != tc.wantStatus || f.fallback != tc.wantCalls {
				t.Fatalf("fallback = %d %s calls=%d", rec.Code, rec.Body.String(), f.fallback)
			}
			if tc.wantCalls == 1 && rec.Body.String() != `{"id":"0123456789abcdef0123456789abcdef","name":"Unknown"}` {
				t.Fatalf("official bytes changed: %s", rec.Body.String())
			}
			events, err := f.st.ListSAJoinEvents(context.Background(), f.tenant, f.server, f.now.Add(-time.Minute), f.now.Add(time.Minute), 20)
			if err != nil || len(events) != 1 || events[0].ReasonCode != tc.wantReason {
				t.Fatalf("fallback event = %+v, %v", events, err)
			}
		})
	}
}

func TestServerAccessFaultAndInvalidArgumentsNeverFallBack(t *testing.T) {
	for _, tc := range []struct {
		name, drop, username, reason string
	}{
		{name: "join database fault", drop: "ygg_join_sessions", username: "Alice", reason: "store_fault"},
		{name: "policy database fault", drop: "sa_server_bans", username: "Alice", reason: "policy_fault"},
		{name: "fallback option database fault", drop: "options", username: "Unknown", reason: "policy_fault"},
		{name: "invalid arguments", username: "", reason: "invalid_arguments"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newVerifyFixture(t)
			f.credential(t, "url_secret")
			f.playerAndJoin(t)
			if tc.drop != "" {
				if _, err := f.st.DB().Exec(`DROP TABLE ` + tc.drop); err != nil {
					t.Fatal(err)
				}
			}
			rec := f.verify(t, "url_secret", tc.username, "server-id", "")
			if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 || f.fallback != 0 {
				t.Fatalf("fault = %d %s fallback=%d", rec.Code, rec.Body.String(), f.fallback)
			}
			events, err := f.st.ListSAJoinEvents(context.Background(), f.tenant, f.server, f.now.Add(-time.Minute), f.now.Add(time.Minute), 20)
			if err != nil || len(events) != 1 || events[0].ReasonCode != tc.reason {
				t.Fatalf("fault event = %+v, %v", events, err)
			}
		})
	}
}
func TestServerAccessQuotaDeniesBeforeFallback(t *testing.T) {
	f := newVerifyFixture(t)
	f.credential(t, "url_secret")
	f.playerAndJoin(t)
	limit := 1
	if err := f.st.UpdateSAServerPlatformPolicy(context.Background(), f.tenant, f.server, &limit, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if rec := f.verify(t, "url_secret", "Alice", "server-id", "203.0.113.8"); rec.Code != http.StatusOK {
		t.Fatalf("first request = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.verify(t, "url_secret", "Unknown", "server-id", ""); rec.Code != http.StatusNoContent || f.fallback != 0 {
		t.Fatalf("quota denial = %d %s fallback=%d", rec.Code, rec.Body.String(), f.fallback)
	}
	events, err := f.st.ListSAJoinEvents(context.Background(), f.tenant, f.server, f.now.Add(-time.Minute), f.now.Add(time.Minute), 20)
	if err != nil || len(events) != 2 {
		t.Fatalf("quota events = %+v, %v", events, err)
	}
	found := false
	for _, event := range events {
		if event.ReasonCode == "quota_exceeded" {
			found = true
		}
	}
	if !found {
		t.Fatalf("quota reason missing: %+v", events)
	}
}

func TestServerAccessEventFailureDegradesAndLogsFixedLabel(t *testing.T) {
	f := newVerifyFixture(t)
	f.credential(t, "url_secret")
	f.playerAndJoin(t)
	if _, err := f.st.DB().Exec(`CREATE TRIGGER fail_sa_events BEFORE INSERT ON sa_join_events BEGIN SELECT RAISE(ABORT,'forced event failure'); END`); err != nil {
		t.Fatal(err)
	}
	var logs strings.Builder
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	hooks := 0
	f.stack.cfg.OnEventError = func(context.Context, store.SAJoinEvent, error) {
		hooks++
		panic(f.secret)
	}
	f.h = f.stack.Gate(f.stack.Handler())
	failuresBefore := metrics.ServerAccessEventWriteFailures.Value()
	r := httptest.NewRequest(http.MethodGet, "/v/"+f.secret+"/sessionserver/session/minecraft/hasJoined?username=Alice&serverId=server-id&ip=203.0.113.8", nil)
	r.Header.Set("Authorization", "Bearer "+f.secret)
	r.Header.Set("X-Request-Id", "req-safe")
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	if rec.Code != http.StatusOK || hooks != 1 {
		t.Fatalf("event failure blocked verification: status=%d hooks=%d", rec.Code, hooks)
	}
	if got := metrics.ServerAccessEventWriteFailures.Value(); got != failuresBefore+1 {
		t.Fatalf("event failure metric = %d, want %d", got, failuresBefore+1)
	}
	text := logs.String()
	if !strings.Contains(text, "serveraccess.verify event_write_failed") || !strings.Contains(text, "serveraccess.verify event_error_hook_panic") {
		t.Fatalf("missing structured event error: %s", text)
	}
	for _, sensitive := range []string{f.secret, "/v/", "username=", "serverId=", "203.0.113.8", "Alice", "owner@example.com", "Authorization"} {
		if strings.Contains(text, sensitive) {
			t.Fatalf("secret or diagnostic data leaked in log: %s", text)
		}
	}
}

func TestServerAccessWireRegressionAndHeaderEventSource(t *testing.T) {
	f := newVerifyFixture(t)
	f.credential(t, "header")
	f.playerAndJoin(t)
	rec := f.verify(t, "header", "Alice", "server-id", "203.0.113.8")
	if rec.Code != http.StatusOK {
		t.Fatalf("profile status = %d %s", rec.Code, rec.Body.String())
	}
	var profile ygg.Profile
	if err := json.Unmarshal(rec.Body.Bytes(), &profile); err != nil {
		t.Fatal(err)
	}
	if profile.ID != store.OfflineUUID("Alice") || profile.Name != "Alice" {
		t.Fatalf("standard profile identity changed: %+v", profile)
	}
	var texture *ygg.Property
	for i := range profile.Properties {
		if profile.Properties[i].Name == "textures" {
			texture = &profile.Properties[i]
		}
	}
	if texture == nil || texture.Signature == "" {
		t.Fatalf("signed textures property missing: %+v", profile.Properties)
	}
	textureJSON, err := base64.StdEncoding.DecodeString(texture.Value)
	if err != nil {
		t.Fatal(err)
	}
	var textures struct {
		Timestamp int64 `json:"timestamp"`
	}
	if err := json.Unmarshal(textureJSON, &textures); err != nil || textures.Timestamp <= 0 {
		t.Fatalf("texture timestamp = %+v, %v", textures, err)
	}
	signature, err := base64.StdEncoding.DecodeString(texture.Signature)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(f.signer.PublicKeyPEM()))
	if block == nil {
		t.Fatal("signer public key PEM missing")
	}
	publicKey, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	rsaKey, ok := publicKey.(*rsa.PublicKey)
	if !ok {
		t.Fatalf("signer public key = %T, want RSA", publicKey)
	}
	digest := sha1.Sum([]byte(texture.Value))
	if err := rsa.VerifyPKCS1v15(rsaKey, crypto.SHA1, digest[:], signature); err != nil {
		t.Fatalf("texture signature verification failed: %v", err)
	}
	fallbackOff := false
	if err := f.st.UpdateSAServerOwnerPolicy(context.Background(), f.tenant, f.server, nil, &fallbackOff); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ sid, ip string }{{sid: "wrong-server", ip: "203.0.113.8"}, {sid: "server-id", ip: "203.0.113.9"}} {
		if rec := f.verify(t, "header", "Alice", tc.sid, tc.ip); rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
			t.Fatalf("mismatch %+v = %d %s", tc, rec.Code, rec.Body.String())
		}
	}
	if f.fallback != 0 {
		t.Fatalf("fallback called for mismatch: %d", f.fallback)
	}
	events, err := f.st.ListSAJoinEvents(context.Background(), f.tenant, f.server, f.now.Add(-time.Minute), f.now.Add(time.Minute), 20)
	if err != nil || len(events) != 3 {
		t.Fatalf("header event = %+v, %v", events, err)
	}
	for _, event := range events {
		if event.SourceMode != "header" || event.TrustLevel() != "header_credential" {
			t.Fatalf("header source mislabeled: %+v", event)
		}
	}
}

func TestServerAccessDisabledModuleReturns404ForBothCredentialModes(t *testing.T) {
	f := newVerifyFixture(t)
	f.credential(t, "url_secret")
	f.playerAndJoin(t)
	f.stack.SetEnabled(false)
	for _, mode := range []string{"url_secret", "header"} {
		rec := f.verify(t, mode, "Alice", "server-id", "203.0.113.8")
		if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), f.secret) || f.fallback != 0 {
			t.Fatalf("disabled %s = %d %s fallback=%d", mode, rec.Code, rec.Body.String(), f.fallback)
		}
		if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Referrer-Policy") != "no-referrer" {
			t.Fatalf("disabled %s security headers = %v", mode, rec.Header())
		}
	}
}

func TestServerAccessConcurrencyFollowsPolicyOrderAndBoundsFallback(t *testing.T) {
	f := newVerifyFixture(t)
	f.credential(t, "url_secret")
	if _, _, err := f.st.CreatePlayer(context.Background(), f.owner, "Alice"); err != nil {
		t.Fatal(err)
	}
	serverFallback := true
	limit := 1
	if err := f.st.UpdateSAServerOwnerPolicy(context.Background(), f.tenant, f.server, nil, &serverFallback); err != nil {
		t.Fatal(err)
	}
	if err := f.st.UpdateSAServerPlatformPolicy(context.Background(), f.tenant, f.server, nil, nil, &limit, nil, nil); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	releaseFallback := make(chan struct{})
	f.api.SetFallback(&ygg.Fallback{BaseURL: "https://official.example", Enabled: func(context.Context) bool { return true }, Client: &http.Client{Transport: verifyFallback(func(*http.Request) (*http.Response, error) {
		close(started)
		<-releaseFallback
		return verifyFallbackResponse(`{"id":"0123456789abcdef0123456789abcdef","name":"Alice"}`), nil
	})}})
	firstDone := make(chan *httptest.ResponseRecorder, 1)
	go func() { firstDone <- f.verify(t, "url_secret", "Alice", "server-id", "203.0.113.8") }()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first verification did not reach fallback")
	}
	if err := f.st.SetUserPermission(context.Background(), f.owner, -1); err != nil {
		t.Fatal(err)
	}
	if rec := f.verify(t, "url_secret", "Alice", "server-id", "203.0.113.8"); rec.Code != http.StatusNoContent {
		t.Fatalf("platform ban under concurrency = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.verify(t, "url_secret", "Unknown", "server-id", ""); rec.Code != http.StatusNoContent {
		t.Fatalf("concurrent limit = %d %s", rec.Code, rec.Body.String())
	}
	close(releaseFallback)
	select {
	case rec := <-firstDone:
		if rec.Code != http.StatusOK {
			t.Fatalf("blocked first verification = %d %s", rec.Code, rec.Body.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first verification did not finish")
	}
	events, err := f.st.ListSAJoinEvents(context.Background(), f.tenant, f.server, f.now.Add(-time.Minute), f.now.Add(time.Minute), 20)
	if err != nil || len(events) != 3 {
		t.Fatalf("concurrency events = %+v, %v", events, err)
	}
	reasons := map[string]bool{}
	for _, event := range events {
		reasons[event.ReasonCode] = true
	}
	if !reasons["platform_banned"] || !reasons["concurrent_limit"] || !reasons["official_fallback"] {
		t.Fatalf("concurrency reasons = %+v", events)
	}
}
func TestServerAccessInvalidExpiredRevokedAndRotatedCredentialsReturn404(t *testing.T) {
	for _, tc := range []string{"unknown-secret", "missing-scope", "disabled-credential", "expired", "revoked", "rotated-expired", "disabled-server", "disabled-tenant", "disabled-module"} {
		t.Run(tc, func(t *testing.T) {
			f := newVerifyFixture(t)
			id := f.credential(t, "url_secret")
			switch tc {
			case "unknown-secret":
				f.secret = "unknown-only-secret-0123456789abcdef"
			case "missing-scope":
				if _, err := f.st.DB().Exec(`UPDATE sa_server_credentials SET scopes='[]' WHERE id=?`, id); err != nil {
					t.Fatal(err)
				}
			case "disabled-credential":
				if err := f.st.SetSAServerCredentialStatus(context.Background(), f.tenant, f.server, id, "disabled"); err != nil {
					t.Fatal(err)
				}
			case "expired":
				if _, err := f.st.DB().Exec(`UPDATE sa_server_credentials SET expires_at=? WHERE id=?`, f.now.Add(-time.Second), id); err != nil {
					t.Fatal(err)
				}
			case "revoked":
				if err := f.st.RevokeSAServerCredential(context.Background(), f.tenant, f.server, id, f.now); err != nil {
					t.Fatal(err)
				}
			case "rotated-expired":
				sum := sha256.Sum256([]byte("replacement-only-secret-0123456789abcdef"))
				fresh := store.SACredential{Mode: "url_secret", SecretHash: hex.EncodeToString(sum[:]), Scopes: `["verify:join"]`, Status: "active", ExpiresAt: sql.NullTime{Time: f.now.Add(48 * time.Hour), Valid: true}}
				if _, err := f.st.RotateSAServerCredential(context.Background(), f.tenant, f.server, id, fresh, 24*time.Hour, f.now); err != nil {
					t.Fatal(err)
				}
				f.now = f.now.Add(25 * time.Hour)
			case "disabled-server":
				if err := f.st.UpdateSAServer(context.Background(), f.tenant, f.server, "one", "disabled"); err != nil {
					t.Fatal(err)
				}
			case "disabled-tenant":
				if err := f.st.UpdateSATenantStatus(context.Background(), f.tenant, "suspended"); err != nil {
					t.Fatal(err)
				}
			case "disabled-module":
				f.stack.SetEnabled(false)
			}
			rec := f.verify(t, "url_secret", "Alice", "server-id", "")
			if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), f.secret) || f.fallback != 0 {
				t.Fatalf("invalid credential = %d %s fallback=%d", rec.Code, rec.Body.String(), f.fallback)
			}
		})
	}
}
