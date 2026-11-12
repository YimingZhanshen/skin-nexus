package oidc

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"testing"
	"time"

	oidclib "github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// flakyBackend fails chosen backend calls with a genuine store fault (not
// ErrNotFound), so the storage layer must answer with a server_error rather
// than misreporting the outage as a client fault.
type flakyBackend struct {
	Backend
	failGetClientCalls  int // fail GetClient on the Nth call (1-based); 0 = never
	failGetClientCount  int
	failGetRefreshToken string // fail GetRefreshToken only for this token
}

func (b *flakyBackend) GetClient(ctx context.Context, id string) (ClientRecord, error) {
	b.failGetClientCount++
	if b.failGetClientCalls > 0 && b.failGetClientCount == b.failGetClientCalls {
		return ClientRecord{}, errors.New("store: connection reset")
	}
	return b.Backend.GetClient(ctx, id)
}

// EnsureOidcClient is not part of the Backend interface (memBackend-only), so
// SeedSharedClient's type assertion needs it promoted on the wrapper too.
func (b *flakyBackend) EnsureOidcClient(ctx context.Context, rec ClientRecord) error {
	return b.Backend.(interface {
		EnsureOidcClient(context.Context, ClientRecord) error
	}).EnsureOidcClient(ctx, rec)
}

func (b *flakyBackend) GetRefreshToken(ctx context.Context, token string) (RefreshTokenRecord, error) {
	if b.failGetRefreshToken != "" && token == b.failGetRefreshToken {
		return RefreshTokenRecord{}, errors.New("store: connection reset")
	}
	return b.Backend.GetRefreshToken(ctx, token)
}

func decodeErrBody(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("error body is not JSON: %v (%s)", err, body)
	}
	return m
}

// faultProvider builds the provider around fb so the faulting backend is the
// one Storage actually calls (assigning p.backend after New() would not reach
// the reference Storage captured during construction).
func faultProvider(t *testing.T, fb *flakyBackend) *Provider {
	t.Helper()
	p, err := New(fb, ProfileSource{
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
	return p
}

// A store fault while looking up the refresh token must surface as a 500
// server_error: an invalid_grant here tells the client the token is dead and
// it will discard a perfectly good session over a database blip.
func TestRefreshGrantStoreFaultIs500(t *testing.T) {
	fb := &flakyBackend{Backend: newMemBackend(), failGetRefreshToken: "rt-anything"}
	p := faultProvider(t, fb)
	h := p.Wrap(http.NotFoundHandler())

	r := form(t, h, "POST", "/oauth/token", url.Values{
		"client_id":     {"2"},
		"grant_type":    {"refresh_token"},
		"refresh_token": {"rt-anything"},
	})
	body := decodeErrBody(t, r.Body.String())
	if r.Code != http.StatusInternalServerError || body["error"] != "server_error" {
		t.Fatalf("store fault on refresh lookup = %d %v, want 500 server_error (client would discard the token)", r.Code, body)
	}
}

// An unknown or expired refresh token stays an invalid_grant: the control half
// of the pair above, proving the 500 is not just "everything is 500 now".
func TestRefreshGrantUnknownTokenIsInvalidGrant(t *testing.T) {
	p, _ := testProvider(t)
	h := p.Wrap(http.NotFoundHandler())

	r := form(t, h, "POST", "/oauth/token", url.Values{
		"client_id":     {"2"},
		"grant_type":    {"refresh_token"},
		"refresh_token": {"rt-does-not-exist"},
	})
	body := decodeErrBody(t, r.Body.String())
	if r.Code != http.StatusBadRequest || body["error"] != "invalid_grant" {
		t.Fatalf("unknown refresh token = %d %v, want 400 invalid_grant", r.Code, body)
	}
}

// A store fault during client authentication (AuthorizeClientIDSecret) must
// answer 500 server_error, not 401 invalid_client: zitadel's clientAuthError
// only promotes errors whose chain carries an oidc.ServerError.
func TestClientAuthStoreFaultIs500(t *testing.T) {
	_, b := testProvider(t)
	// Confidential client so AuthMethodPost routes through
	// AuthorizeClientIDSecret after the initial GetClientByClientID succeeds.
	if err := b.EnsureOidcClient(context.Background(), ClientRecord{
		ID: "conf", SecretHash: "s3cret-hash",
		GrantTypes: []string{"refresh_token"}, Name: "conf",
	}); err != nil {
		t.Fatal(err)
	}
	// First GetClient (GetClientByClientID) must succeed; the second (inside
	// AuthorizeClientIDSecret) carries the fault.
	fb := &flakyBackend{Backend: b, failGetClientCalls: 2}
	p := faultProvider(t, fb)
	h := p.Wrap(http.NotFoundHandler())

	r := form(t, h, "POST", "/oauth/token", url.Values{
		"client_id":     {"conf"},
		"client_secret": {"whatever"},
		"grant_type":    {"refresh_token"},
		"refresh_token": {"rt-x"},
	})
	body := decodeErrBody(t, r.Body.String())
	if r.Code != http.StatusInternalServerError || body["error"] != "server_error" {
		t.Fatalf("store fault during client auth = %d %v, want 500 server_error (401 would blame the client's secret)", r.Code, body)
	}
}

// Wrong client secret stays 401: the control half proving the 500 above is
// fault-vs-credential discrimination, not a blanket status change.
func TestClientAuthWrongSecretIs401(t *testing.T) {
	p, b := testProvider(t)
	if err := b.EnsureOidcClient(context.Background(), ClientRecord{
		ID: "conf2", SecretHash: "right-hash",
		GrantTypes: []string{"refresh_token"}, Name: "conf2",
	}); err != nil {
		t.Fatal(err)
	}
	h := p.Wrap(http.NotFoundHandler())

	r := form(t, h, "POST", "/oauth/token", url.Values{
		"client_id":     {"conf2"},
		"client_secret": {"wrong"},
		"grant_type":    {"refresh_token"},
		"refresh_token": {"rt-x"},
	})
	body := decodeErrBody(t, r.Body.String())
	if r.Code != http.StatusUnauthorized || body["error"] != "invalid_client" {
		t.Fatalf("wrong secret = %d %v, want 401 invalid_client", r.Code, body)
	}
}

// Storage-level contract: AuthorizeClientIDSecret must classify a store fault
// as oidc.ServerError (so zitadel's clientAuthError answers 500) while an
// unknown client stays a plain error (answers 401).
func TestAuthorizeClientIDSecretClassifiesErrors(t *testing.T) {
	b := newMemBackend()
	fb := &flakyBackend{Backend: b, failGetClientCalls: 1}
	s := NewStorage(fb, ProfileSource{}, testKey(t), "kid", "https://skin.test")

	err := s.AuthorizeClientIDSecret(context.Background(), "any", "any")
	if err == nil {
		t.Fatal("store fault must surface as an error")
	}
	var oe *oidclib.Error
	if !errors.As(err, &oe) || oe.ErrorType != oidclib.ServerError {
		t.Fatalf("store fault error = %#v, want oidc.ServerError in chain (zitadel clientAuthError keys off this)", err)
	}

	// Unknown client stays plain: clientAuthError must map it to invalid_client.
	err = s.AuthorizeClientIDSecret(context.Background(), "ghost", "any")
	if err == nil {
		t.Fatal("unknown client must error")
	}
	if errors.As(err, &oe) {
		t.Fatalf("unknown client error = %#v, want plain error (401 invalid_client), got %T", err, err)
	}
}

// Storage-level contract: a store fault while reading the refresh token must
// NOT be reported as ErrInvalidRefreshToken — that sentinel means "the token
// is bad", which zitadel turns into a 400 the client acts on.
func TestTokenRequestByRefreshTokenClassifiesErrors(t *testing.T) {
	b := newMemBackend()
	fb := &flakyBackend{Backend: b, failGetRefreshToken: "rt"}
	s := NewStorage(fb, ProfileSource{}, testKey(t), "kid", "https://skin.test")

	_, err := s.TokenRequestByRefreshToken(context.Background(), "rt")
	if err == nil {
		t.Fatal("store fault must surface as an error")
	}
	if errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Fatal("store fault reported as ErrInvalidRefreshToken — client would discard a live token over a DB blip")
	}
	var oe *oidclib.Error
	if !errors.As(err, &oe) || oe.ErrorType != oidclib.ServerError {
		t.Fatalf("store fault error = %#v, want oidc.ServerError in chain", err)
	}

	// Unknown token keeps the sentinel.
	_, err = s.TokenRequestByRefreshToken(context.Background(), "rt-missing")
	if !errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Fatalf("unknown token error = %v, want ErrInvalidRefreshToken", err)
	}
}

// A store fault while reading refresh-token info (revocation path) goes
// through the same GetRefreshToken backend call, so the same classification
// applies: zitadel treats the ErrInvalidRefreshToken sentinel as "token is
// bad" and answers 400 for what is actually an outage.
func TestGetRefreshTokenInfoClassifiesErrors(t *testing.T) {
	b := newMemBackend()
	fb := &flakyBackend{Backend: b, failGetRefreshToken: "rt"}
	s := NewStorage(fb, ProfileSource{}, testKey(t), "kid", "https://skin.test")

	_, _, err := s.GetRefreshTokenInfo(context.Background(), "c", "rt")
	if err == nil {
		t.Fatal("store fault must surface as an error")
	}
	if errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Fatal("store fault reported as ErrInvalidRefreshToken — revocation would answer 400 instead of 500")
	}
}

// A store fault during token rotation (GetRefreshToken inside
// CreateAccessAndRefreshTokens) must not masquerade as ErrInvalidRefreshToken
// either — same client-discard hazard as the refresh lookup.
func TestCreateTokensRotationStoreFaultNotInvalidToken(t *testing.T) {
	b := newMemBackend()
	fb := &flakyBackend{Backend: b, failGetRefreshToken: "rt-old"}
	s := NewStorage(fb, ProfileSource{}, testKey(t), "kid", "https://skin.test")

	req := &refreshTokenRequest{rec: RefreshTokenRecord{
		Token: "rt-old", UserID: 42, ClientID: "2",
		Scopes: []string{"openid"}, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	}}
	_, _, _, err := s.CreateAccessAndRefreshTokens(context.Background(), req, "rt-old")
	if err == nil {
		t.Fatal("store fault must surface as an error")
	}
	if errors.Is(err, op.ErrInvalidRefreshToken) {
		t.Fatal("rotation store fault reported as ErrInvalidRefreshToken")
	}
	var oe *oidclib.Error
	if !errors.As(err, &oe) || oe.ErrorType != oidclib.ServerError {
		t.Fatalf("rotation store fault = %#v, want oidc.ServerError in chain", err)
	}
}
