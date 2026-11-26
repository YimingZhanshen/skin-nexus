package oidc

import (
	"context"
	"crypto"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"skin-nexus/server/internal/ygg"
)

// ScopeYggdrasilJoin is the scope that authorises a token to be used as a
// Yggdrasil session token (game join). Launcher scope set is fixed to
// openid/offline_access/PlayerProfiles.Select/Server.Join (both launchers).
const ScopeYggdrasilJoin = "Yggdrasil.Server.Join"

// TokenPolicy configures access-token audience validation and the Yggdrasil
// join-client allowlist. Audience is required for both Bearer verification and
// Yggdrasil resolution; AllowedClientIDs is required only for Yggdrasil
// resolution. An empty/incomplete Yggdrasil policy fails closed.
type TokenPolicy struct {
	Audience         string
	AllowedClientIDs []string
}

func normalizeTokenPolicy(policy TokenPolicy) TokenPolicy {
	policy.Audience = strings.TrimSpace(policy.Audience)
	seen := make(map[string]struct{}, len(policy.AllowedClientIDs))
	clients := make([]string, 0, len(policy.AllowedClientIDs))
	for _, id := range policy.AllowedClientIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		clients = append(clients, id)
	}
	policy.AllowedClientIDs = clients
	return policy
}

func (p TokenPolicy) valid() bool {
	return p.Audience != "" && len(p.AllowedClientIDs) > 0
}

func (p TokenPolicy) allowsClient(id string) bool {
	for _, allowed := range p.AllowedClientIDs {
		if id == allowed {
			return true
		}
	}
	return false
}

// ErrNotJOSE reports that the presented credential is not a JWT (callers fall
// back to the opaque yggdrasil token lookup).
var ErrNotJOSE = errors.New("oidc: not a JWT")

// NewYggTokenResolver returns a resolver for ygg.Service.SetTokenResolver.
// It accepts OIDC-issued RS256 JWT access tokens as Yggdrasil tokens — the
// dual-state requirement proven by both launchers: the OAuth access_token is
// passed to the game verbatim and used against /sessionserver/join and
// /authserver/validate.
//
// Acceptance rules (OIDC client contract):
//   - signature RS256 against the provider signing key;
//   - iss == configured issuer, aud contains the global Yggdrasil resource,
//     exp within 60s skew;
//   - jti present in the ledger, not revoked, and its client_id is in the
//     configured allowlist (revocation covers password change / signout);
//   - scope contains Yggdrasil.Server.Join (a token without join scope is not
//     a Yggdrasil credential at all);
//   - the returned ygg.Token uses the normalised key "jti:<jti>" as
//     AccessToken so join sessions stay short-keyed.
func NewYggTokenResolver(backend Backend, issuer string, pub *rsa.PublicKey, policy TokenPolicy) func(context.Context, string) (ygg.Token, error) {
	policy = normalizeTokenPolicy(policy)
	return func(ctx context.Context, raw string) (ygg.Token, error) {
		return resolveJWT(ctx, backend, issuer, pub, policy, raw, true)
	}
}

// VerifyBearer verifies an access token for the userinfo endpoint and returns
// its ledger record. It enforces the resource audience and common token checks,
// but not the Yggdrasil join scope or client allowlist.
func VerifyBearer(ctx context.Context, backend Backend, issuer string, pub *rsa.PublicKey, raw string, policy TokenPolicy) (AccessTokenRecord, error) {
	rec, _, err := verifyJWT(ctx, backend, issuer, pub, normalizeTokenPolicy(policy), raw, false)
	if err != nil {
		return AccessTokenRecord{}, err
	}
	return rec, nil
}

func resolveJWT(ctx context.Context, backend Backend, issuer string, pub *rsa.PublicKey, policy TokenPolicy, raw string, forYggdrasil bool) (ygg.Token, error) {
	rec, _, err := verifyJWT(ctx, backend, issuer, pub, policy, raw, forYggdrasil)
	if err != nil {
		return ygg.Token{}, err
	}
	return ygg.Token{
		AccessToken: JWTKey(rec.JTI),
		UserID:      rec.UserID,
		SelectedPID: rec.SelectedPID,
		CreatedAt:   rec.CreatedAt,
		ExpiresAt:   rec.ExpiresAt,
	}, nil
}

// audienceClaim accepts both JWT representations allowed by RFC 7519: a
// single string or an array of strings. Missing/null ultimately fails the
// required-resource membership check.
type audienceClaim []string

func (a *audienceClaim) UnmarshalJSON(data []byte) error {
	var single string
	if err := json.Unmarshal(data, &single); err == nil {
		*a = audienceClaim{single}
		return nil
	}
	var list []string
	if err := json.Unmarshal(data, &list); err != nil {
		return err
	}
	*a = audienceClaim(list)
	return nil
}

type jwtClaims struct {
	Issuer   string        `json:"iss"`
	Subject  string        `json:"sub"`
	Audience audienceClaim `json:"aud"`
	Exp      int64         `json:"exp"`
	Iat      int64         `json:"iat"`
	JTI      string        `json:"jti"`
	Scope    string        `json:"scope"`
}

// verifyJWT is the single verification path: signature first, then claims,
// then the jti ledger. There is intentionally no "skip signature" fallback.
func verifyJWT(ctx context.Context, backend Backend, issuer string, pub *rsa.PublicKey, policy TokenPolicy, raw string, forYggdrasil bool) (AccessTokenRecord, jwtClaims, error) {
	var zero AccessTokenRecord
	parts := strings.Split(raw, ".")
	if len(parts) != 3 {
		return zero, jwtClaims{}, ErrNotJOSE
	}
	header, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return zero, jwtClaims{}, ErrNotJOSE
	}
	var hdr struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(header, &hdr); err != nil || hdr.Alg != "RS256" {
		return zero, jwtClaims{}, ErrNotJOSE
	}

	sig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return zero, jwtClaims{}, ygg.ErrTokenInvalid
	}
	h := crypto.SHA256.New()
	_, _ = h.Write([]byte(parts[0] + "." + parts[1]))
	if err := rsa.VerifyPKCS1v15(pub, crypto.SHA256, h.Sum(nil), sig); err != nil {
		return zero, jwtClaims{}, ygg.ErrTokenInvalid
	}

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return zero, jwtClaims{}, ygg.ErrTokenInvalid
	}
	var claims jwtClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return zero, jwtClaims{}, ygg.ErrTokenInvalid
	}
	if claims.Issuer != issuer || claims.JTI == "" || claims.Subject == "" {
		return zero, jwtClaims{}, ygg.ErrTokenInvalid
	}
	if policy.Audience == "" || (forYggdrasil && len(policy.AllowedClientIDs) == 0) || !audienceContains(claims.Audience, policy.Audience) {
		return zero, jwtClaims{}, ygg.ErrTokenInvalid
	}
	now := time.Now()
	if now.After(time.Unix(claims.Exp, 0).Add(60 * time.Second)) {
		return zero, jwtClaims{}, ygg.ErrTokenInvalid
	}

	rec, err := backend.GetAccessToken(ctx, claims.JTI)
	if err != nil {
		return zero, jwtClaims{}, ygg.ErrTokenInvalid
	}
	if !rec.RevokedAt.IsZero() || now.After(rec.ExpiresAt) {
		return zero, jwtClaims{}, ygg.ErrTokenInvalid
	}
	// A ledger client outside the allowlist is not a Yggdrasil credential.
	// Standard OIDC userinfo intentionally has no launcher-client restriction.
	if forYggdrasil && !policy.allowsClient(rec.ClientID) {
		return zero, jwtClaims{}, ygg.ErrTokenInvalid
	}
	// Join-scope gate. The ledger record is authoritative: zitadel v3 JWT
	// access tokens may omit the "scope" claim entirely (CreateJWT never sets
	// Scopes), so the claim is only a fallback.
	if forYggdrasil {
		granted := scopeGranted(claims.Scope, ScopeYggdrasilJoin) ||
			scopeGranted(strings.Join(rec.Scopes, " "), ScopeYggdrasilJoin)
		if !granted {
			return zero, jwtClaims{}, ygg.ErrTokenInvalid
		}
	}
	return rec, claims, nil
}

// JWTKey normalises a JWT to the short key used in join-session rows.
func JWTKey(jti string) string { return "jti:" + jti }

func audienceContains(audience []string, want string) bool {
	for _, got := range audience {
		if got == want {
			return true
		}
	}
	return false
}

func scopeGranted(scopeList, want string) bool {
	for _, s := range strings.Fields(scopeList) {
		if s == want {
			return true
		}
	}
	return false
}
