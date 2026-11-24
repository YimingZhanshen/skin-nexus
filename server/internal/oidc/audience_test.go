package oidc

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestZitadelProviderIssuesYggdrasilResourceAudience(t *testing.T) {
	p, _ := testProvider(t)
	h := p.Wrap(http.NotFoundHandler())
	rec := form(t, h, http.MethodPost, "/device_authorization", url.Values{
		"client_id": {"2"},
		"scope":     {"openid offline_access Yggdrasil.PlayerProfiles.Select Yggdrasil.Server.Join"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("device_authorization = %d %s", rec.Code, rec.Body.String())
	}
	var device struct {
		DeviceCode string `json:"device_code"`
		UserCode   string `json:"user_code"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &device); err != nil {
		t.Fatalf("decode device authorization: %v", err)
	}
	if err := p.ApproveDevice(context.Background(), 42, device.UserCode, 7); err != nil {
		t.Fatalf("approve device: %v", err)
	}
	poll := func(values url.Values) map[string]any {
		t.Helper()
		r := form(t, h, http.MethodPost, "/oauth/token", values)
		if r.Code != http.StatusOK {
			t.Fatalf("token exchange = %d %s", r.Code, r.Body.String())
		}
		var body map[string]any
		if err := json.Unmarshal(r.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode token response: %v", err)
		}
		return body
	}
	first := poll(url.Values{
		"client_id":   {"2"},
		"grant_type":  {"urn:ietf:params:oauth:grant-type:device_code"},
		"device_code": {device.DeviceCode},
	})
	access, _ := first["access_token"].(string)
	refresh, _ := first["refresh_token"].(string)
	if access == "" || refresh == "" {
		t.Fatalf("token response missing access/refresh: %v", first)
	}
	wantAudience := "https://skin.test/api/yggdrasil"
	assertAccessAudience := func(raw string) {
		t.Helper()
		verifyJWTSignature(t, &p.cfg.Key.PublicKey, raw)
		claims := decodeJWTClaims(t, raw)
		if got := audienceValues(claims["aud"]); !slices.Contains(got, wantAudience) {
			t.Fatalf("provider access-token aud = %v, want global resource %q", got, wantAudience)
		}
	}
	assertAccessAudience(access)

	second := poll(url.Values{
		"client_id":     {"2"},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refresh},
	})
	rotated, _ := second["access_token"].(string)
	if rotated == "" {
		t.Fatalf("refresh response missing access_token: %v", second)
	}
	assertAccessAudience(rotated)
}

func audienceValues(raw any) []string {
	switch v := raw.(type) {
	case string:
		return []string{v}
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func signedTestJWT(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT"})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		t.Fatal(err)
	}
	unsigned := base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(payload)
	digest := sha256.Sum256([]byte(unsigned))
	signature, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	return unsigned + "." + base64.RawURLEncoding.EncodeToString(signature)
}

func validAccessClaims(jti string) map[string]any {
	return map[string]any{
		"iss":   "https://skin.test",
		"sub":   "42",
		"aud":   []string{"https://skin.test/api/yggdrasil", "2"},
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Add(-time.Minute).Unix(),
		"jti":   jti,
		"scope": ScopeYggdrasilJoin,
	}
}

func TestYggTokenResolverRejectsMissingOrWrongAudience(t *testing.T) {
	key := testKey(t)
	backend := newMemBackend()
	resolve := NewYggTokenResolver(backend, "https://skin.test", &key.PublicKey, TokenPolicy{
		Audience:         "https://skin.test/api/yggdrasil",
		AllowedClientIDs: []string{"2"},
	})
	for _, tc := range []struct {
		name     string
		audience any
		omit     bool
	}{
		{name: "missing", omit: true},
		{name: "wrong", audience: "https://elsewhere.test/api/yggdrasil"},
		{name: "wrong type", audience: 42},
	} {
		t.Run(tc.name, func(t *testing.T) {
			claims := validAccessClaims("jti-" + strings.ReplaceAll(tc.name, " ", "-"))
			if tc.omit {
				delete(claims, "aud")
			} else {
				claims["aud"] = tc.audience
			}
			if err := backend.SaveAccessToken(context.Background(), AccessTokenRecord{
				JTI: claims["jti"].(string), UserID: 42, ClientID: "2",
				Scopes: []string{ScopeYggdrasilJoin}, ExpiresAt: time.Now().Add(time.Hour),
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := resolve(context.Background(), signedTestJWT(t, key, claims)); err == nil {
				t.Fatal("token with missing/wrong audience must not resolve")
			}
		})
	}
}

func TestYggTokenResolverEnforcesLedgerClientAllowlist(t *testing.T) {
	key := testKey(t)
	backend := newMemBackend()
	policy := TokenPolicy{
		Audience:         "https://skin.test/api/yggdrasil",
		AllowedClientIDs: []string{"2", "launcher"},
	}
	resolve := NewYggTokenResolver(backend, "https://skin.test", &key.PublicKey, policy)
	for _, tc := range []struct {
		name     string
		clientID string
		wantOK   bool
	}{
		{name: "shared client", clientID: "2", wantOK: true},
		{name: "explicitly allowed client", clientID: "launcher", wantOK: true},
		{name: "wrong client", clientID: "other"},
		{name: "empty client", clientID: ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jti := "jti-" + strings.ReplaceAll(tc.name, " ", "-")
			claims := validAccessClaims(jti)
			claims["aud"] = []string{policy.Audience, tc.clientID}
			if err := backend.SaveAccessToken(context.Background(), AccessTokenRecord{
				JTI: jti, UserID: 42, ClientID: tc.clientID,
				Scopes: []string{ScopeYggdrasilJoin}, ExpiresAt: time.Now().Add(time.Hour),
			}); err != nil {
				t.Fatal(err)
			}
			_, err := resolve(context.Background(), signedTestJWT(t, key, claims))
			if tc.wantOK && err != nil {
				t.Fatalf("allowed client rejected: %v", err)
			}
			if !tc.wantOK && err == nil {
				t.Fatal("ledger client outside the allowlist must not resolve")
			}
		})
	}
}

func TestYggTokenResolverAcceptsSingleStringAudience(t *testing.T) {
	key := testKey(t)
	backend := newMemBackend()
	claims := validAccessClaims("jti-string-aud")
	claims["aud"] = "https://skin.test/api/yggdrasil"
	if err := backend.SaveAccessToken(context.Background(), AccessTokenRecord{
		JTI: "jti-string-aud", UserID: 42, ClientID: "2",
		Scopes: []string{ScopeYggdrasilJoin}, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	resolve := NewYggTokenResolver(backend, "https://skin.test", &key.PublicKey, TokenPolicy{
		Audience: "https://skin.test/api/yggdrasil", AllowedClientIDs: []string{"2"},
	})
	if _, err := resolve(context.Background(), signedTestJWT(t, key, claims)); err != nil {
		t.Fatalf("single-string aud is valid JWT shape but was rejected: %v", err)
	}
}

func TestYggTokenResolverRejectsExpiredAndRevokedTokens(t *testing.T) {
	key := testKey(t)
	for _, tc := range []struct {
		name      string
		expOffset time.Duration
		revoked   bool
		ledgerExp time.Duration
	}{
		{name: "expired JWT", expOffset: -2 * time.Minute, ledgerExp: time.Hour},
		{name: "expired ledger", expOffset: time.Hour, ledgerExp: -time.Minute},
		{name: "revoked", expOffset: time.Hour, ledgerExp: time.Hour, revoked: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backend := newMemBackend()
			jti := "jti-" + strings.ReplaceAll(tc.name, " ", "-")
			claims := validAccessClaims(jti)
			claims["exp"] = time.Now().Add(tc.expOffset).Unix()
			rec := AccessTokenRecord{
				JTI: jti, UserID: 42, ClientID: "2", Scopes: []string{ScopeYggdrasilJoin},
				ExpiresAt: time.Now().Add(tc.ledgerExp),
			}
			if tc.revoked {
				rec.RevokedAt = time.Now()
			}
			if err := backend.SaveAccessToken(context.Background(), rec); err != nil {
				t.Fatal(err)
			}
			resolve := NewYggTokenResolver(backend, "https://skin.test", &key.PublicKey, TokenPolicy{
				Audience: "https://skin.test/api/yggdrasil", AllowedClientIDs: []string{"2"},
			})
			if _, err := resolve(context.Background(), signedTestJWT(t, key, claims)); err == nil {
				t.Fatal("expired/revoked token must not resolve")
			}
		})
	}
}

func TestYggTokenResolverFailsClosedWithoutPolicy(t *testing.T) {
	key := testKey(t)
	backend := newMemBackend()
	claims := validAccessClaims("jti-no-policy")
	if err := backend.SaveAccessToken(context.Background(), AccessTokenRecord{
		JTI: "jti-no-policy", UserID: 42, ClientID: "2",
		Scopes: []string{ScopeYggdrasilJoin}, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	resolve := NewYggTokenResolver(backend, "https://skin.test", &key.PublicKey, TokenPolicy{})
	if _, err := resolve(context.Background(), signedTestJWT(t, key, claims)); err == nil {
		t.Fatal("empty audience/client policy must fail closed")
	}
}

func TestVerifyBearerUsesSameAudienceAndClientPolicy(t *testing.T) {
	key := testKey(t)
	backend := newMemBackend()
	claims := validAccessClaims("jti-bearer")
	if err := backend.SaveAccessToken(context.Background(), AccessTokenRecord{
		JTI: "jti-bearer", UserID: 42, ClientID: "other",
		Scopes: []string{ScopeYggdrasilJoin}, ExpiresAt: time.Now().Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	raw := signedTestJWT(t, key, claims)
	if _, err := VerifyBearer(context.Background(), backend, "https://skin.test", &key.PublicKey, raw, TokenPolicy{
		Audience: "https://skin.test/api/yggdrasil", AllowedClientIDs: []string{"2"},
	}); err == nil {
		t.Fatal("userinfo verification must enforce the ledger client allowlist")
	}
}
