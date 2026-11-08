package oidc

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"errors"
	"strconv"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/zitadel/oidc/v3/pkg/oidc"
	"github.com/zitadel/oidc/v3/pkg/op"
)

// ---- injected lookups (wired from internal/store in main) ----

// PlayerBrief is the profile row the claim builder needs.
type PlayerBrief struct {
	ID   int64  `json:"id"`
	UUID string `json:"uuid"` // 32 lowercase hex
	Name string `json:"name"`
}

// UserBrief is the user row the claim builder needs.
type UserBrief struct {
	ID            int64
	Nickname      string
	Email         string
	EmailVerified bool
}

// ProfileSource supplies user/player data for claim building. Function fields
// keep the oidc package decoupled from store type signatures.
type ProfileSource struct {
	Players func(ctx context.Context, uid int64) ([]PlayerBrief, error)
	User    func(ctx context.Context, uid int64) (UserBrief, error)
}

// ---- signing keys ----

type signingKey struct {
	id  string
	key *rsa.PrivateKey
}

func (s *signingKey) SignatureAlgorithm() jose.SignatureAlgorithm { return jose.RS256 }
func (s *signingKey) Key() any                                    { return s.key }
func (s *signingKey) ID() string                                  { return s.id }

type publicKey struct {
	id  string
	pub *rsa.PublicKey
}

func (k *publicKey) ID() string                         { return k.id }
func (k *publicKey) Algorithm() jose.SignatureAlgorithm { return jose.RS256 }
func (k *publicKey) Use() string                        { return "sig" }
func (k *publicKey) Key() any                           { return k.pub }

// ---- request wrappers ----

// authRequest wraps AuthRequestRecord to satisfy op.AuthRequest (P1 code flow).
type authRequest struct {
	rec      AuthRequestRecord
	acr      string
	amr      []string
	response oidc.ResponseType
	mode     oidc.ResponseMode
	audience []string
}

func (a *authRequest) GetID() string          { return a.rec.ID }
func (a *authRequest) GetACR() string         { return a.acr }
func (a *authRequest) GetAMR() []string       { return a.amr }
func (a *authRequest) GetAudience() []string  { return a.audience }
func (a *authRequest) GetAuthTime() time.Time { return a.rec.AuthTime }
func (a *authRequest) GetClientID() string    { return a.rec.ClientID }
func (a *authRequest) GetCodeChallenge() *oidc.CodeChallenge {
	if a.rec.CodeChallenge == "" {
		return nil
	}
	return &oidc.CodeChallenge{Challenge: a.rec.CodeChallenge, Method: oidc.CodeChallengeMethodS256}
}
func (a *authRequest) GetNonce() string                   { return a.rec.Nonce }
func (a *authRequest) GetRedirectURI() string             { return a.rec.RedirectURI }
func (a *authRequest) GetResponseType() oidc.ResponseType { return a.response }
func (a *authRequest) GetResponseMode() oidc.ResponseMode { return a.mode }
func (a *authRequest) GetScopes() []string                { return a.rec.Scopes }
func (a *authRequest) GetState() string                   { return a.rec.State }
func (a *authRequest) GetSubject() string                 { return a.rec.Subject }
func (a *authRequest) Done() bool                         { return a.rec.Done }

// refreshTokenRequest satisfies op.RefreshTokenRequest and carries the
// selected profile so rotated tokens keep their claims.
type refreshTokenRequest struct {
	rec      RefreshTokenRecord
	scopes   []string
	audience []string
}

func (r *refreshTokenRequest) GetAMR() []string                 { return nil }
func (r *refreshTokenRequest) GetAudience() []string            { return r.audience }
func (r *refreshTokenRequest) GetAuthTime() time.Time           { return r.rec.AuthTime }
func (r *refreshTokenRequest) GetClientID() string              { return r.rec.ClientID }
func (r *refreshTokenRequest) GetScopes() []string              { return r.scopes }
func (r *refreshTokenRequest) GetSubject() string               { return strconv.FormatInt(r.rec.UserID, 10) }
func (r *refreshTokenRequest) SetCurrentScopes(scopes []string) { r.scopes = scopes }

// ---- the storage adapter ----

// Storage implements op.Storage plus the optional claim hooks and
// op.DeviceAuthorizationStorage. Claim routing (selectedProfile → id_token /
// access token / userinfo) works per grant:
//   - device:  *op.DeviceAuthorizationState is registered in a side map at
//     GetDeviceAuthorizatonState and read back here (v3 API has no PID field);
//   - refresh: refreshTokenRequest carries the PID;
//   - code:    authRequest carries the PID.
type Storage struct {
	backend  Backend
	profiles ProfileSource
	key      *rsa.PrivateKey
	kid      string
	issuer   string

	mu     sync.Mutex
	devPID map[*op.DeviceAuthorizationState]pidEntry
}

type pidEntry struct {
	pid int64
	at  time.Time
}

// NewStorage builds the adapter. kid is the JWK key id echoed in jwks.
func NewStorage(backend Backend, profiles ProfileSource, key *rsa.PrivateKey, kid, issuer string) *Storage {
	return &Storage{
		backend:  backend,
		profiles: profiles,
		key:      key,
		kid:      kid,
		issuer:   issuer,
		devPID:   make(map[*op.DeviceAuthorizationState]pidEntry),
	}
}

// ---- signing / keys ----

func (s *Storage) SigningKey(context.Context) (op.SigningKey, error) {
	return &signingKey{id: s.kid, key: s.key}, nil
}

func (s *Storage) SignatureAlgorithms(context.Context) ([]jose.SignatureAlgorithm, error) {
	return []jose.SignatureAlgorithm{jose.RS256}, nil
}

func (s *Storage) KeySet(context.Context) ([]op.Key, error) {
	return []op.Key{&publicKey{id: s.kid, pub: &s.key.PublicKey}}, nil
}

func (s *Storage) GetKeyByIDAndClientID(_ context.Context, keyID, _ string) (*jose.JSONWebKey, error) {
	if keyID != s.kid {
		return nil, ErrNotFound
	}
	return &jose.JSONWebKey{Key: &s.key.PublicKey, KeyID: s.kid, Algorithm: "RS256", Use: "sig"}, nil
}

func (s *Storage) Health(ctx context.Context) error { return s.backend.Health(ctx) }

// ---- clients ----

func (s *Storage) GetClientByClientID(ctx context.Context, clientID string) (op.Client, error) {
	rec, err := s.backend.GetClient(ctx, clientID)
	if err != nil {
		return nil, err
	}
	return newClient(rec, s.issuer+"/login"), nil
}

func (s *Storage) AuthorizeClientIDSecret(ctx context.Context, clientID, clientSecret string) error {
	rec, err := s.backend.GetClient(ctx, clientID)
	if err != nil {
		return err
	}
	if rec.SecretHash == "" {
		return errors.New("oidc: public client takes no secret")
	}
	if !verifyClientSecret(rec.SecretHash, clientSecret) {
		return errors.New("oidc: invalid client credentials")
	}
	return nil
}

// ---- authorization code flow (P1; wired so grants share one ledger) ----

func (s *Storage) CreateAuthRequest(ctx context.Context, req *oidc.AuthRequest, userID string) (op.AuthRequest, error) {
	uid, _ := strconv.ParseInt(userID, 10, 64)
	rec := AuthRequestRecord{
		ID:                  newID(),
		ClientID:            req.ClientID,
		Subject:             userID,
		Scopes:              req.Scopes,
		RedirectURI:         req.RedirectURI,
		State:               req.State,
		Nonce:               req.Nonce,
		CodeChallenge:       req.CodeChallenge,
		CodeChallengeMethod: string(req.CodeChallengeMethod),
		AuthTime:            time.Now(),
		CreatedAt:           time.Now(),
	}
	_ = uid
	if err := s.backend.SaveAuthRequest(ctx, rec); err != nil {
		return nil, err
	}
	return &authRequest{rec: rec, response: req.ResponseType, mode: req.ResponseMode}, nil
}

func (s *Storage) AuthRequestByID(ctx context.Context, id string) (op.AuthRequest, error) {
	rec, err := s.backend.GetAuthRequest(ctx, id)
	if err != nil {
		return nil, err
	}
	return &authRequest{rec: rec, response: oidc.ResponseTypeCode}, nil
}

func (s *Storage) AuthRequestByCode(ctx context.Context, code string) (op.AuthRequest, error) {
	rec, err := s.backend.GetAuthRequestByCode(ctx, code)
	if err != nil {
		return nil, err
	}
	if rec.CodeExpires.Before(time.Now()) {
		return nil, ErrNotFound
	}
	return &authRequest{rec: rec, response: oidc.ResponseTypeCode}, nil
}

func (s *Storage) SaveAuthCode(ctx context.Context, code, id string) error {
	rec, err := s.backend.GetAuthRequest(ctx, id)
	if err != nil {
		return err
	}
	rec.Code = code
	rec.CodeExpires = time.Now().Add(60 * time.Second) // short codes, legacy parity
	return s.backend.SaveAuthRequest(ctx, rec)
}

func (s *Storage) DeleteAuthRequest(ctx context.Context, id string) error {
	return s.backend.DeleteAuthRequest(ctx, id)
}

// ---- token creation / ledger ----

// pidFor routes the selected profile id out of the grant-specific request.
func (s *Storage) pidFor(request op.TokenRequest) int64 {
	switch r := request.(type) {
	case *op.DeviceAuthorizationState:
		s.mu.Lock()
		defer s.mu.Unlock()
		if e, ok := s.devPID[r]; ok {
			return e.pid
		}
	case *refreshTokenRequest:
		return r.rec.SelectedPID
	case *authRequest:
		return r.rec.SelectedPID
	}
	return 0
}

// clientIDFor digs the client id out of grant-specific request types
// (op.TokenRequest itself has no GetClientID).
func clientIDFor(request any) string {
	type cID interface{ GetClientID() string }
	if c, ok := request.(cID); ok {
		return c.GetClientID()
	}
	return ""
}

func (s *Storage) CreateAccessToken(ctx context.Context, request op.TokenRequest) (string, time.Time, error) {
	exp := time.Now().Add(time.Hour)
	jti := newID()
	rec := AccessTokenRecord{
		JTI:         jti,
		UserID:      subjectUID(request.GetSubject()),
		ClientID:    clientIDFor(request),
		Scopes:      request.GetScopes(),
		SelectedPID: s.pidFor(request),
		ExpiresAt:   exp,
		CreatedAt:   time.Now(),
	}
	if err := s.backend.SaveAccessToken(ctx, rec); err != nil {
		return "", time.Time{}, err
	}
	return jti, exp, nil
}

func (s *Storage) CreateAccessAndRefreshTokens(ctx context.Context, request op.TokenRequest, currentRefreshToken string) (string, string, time.Time, error) {
	jti, exp, err := s.CreateAccessToken(ctx, request)
	if err != nil {
		return "", "", time.Time{}, err
	}
	newRT := newID() + newID() // 64 hex-ish opaque
	rt := RefreshTokenRecord{
		Token:       newRT,
		UserID:      subjectUID(request.GetSubject()),
		ClientID:    clientIDFor(request),
		Scopes:      request.GetScopes(),
		SelectedPID: s.pidFor(request),
		AuthTime:    time.Now(),
		ExpiresAt:   time.Now().Add(30 * 24 * time.Hour),
		CreatedAt:   time.Now(),
	}
	if currentRefreshToken != "" {
		old, gerr := s.backend.GetRefreshToken(ctx, currentRefreshToken)
		if gerr != nil {
			// Unknown refresh token must NOT mint a fresh 30-day pair
			// (review P2: silent fall-through turned stale/rogue tokens
			// into brand-new grants).
			return "", "", time.Time{}, op.ErrInvalidRefreshToken
		}
		if err := s.backend.RotateRefreshToken(ctx, old, rt); err != nil {
			if errors.Is(err, ErrNotFound) {
				return "", "", time.Time{}, op.ErrInvalidRefreshToken // reuse race loser
			}
			return "", "", time.Time{}, err
		}
		return jti, newRT, exp, nil
	}
	if err := s.backend.SaveRefreshToken(ctx, rt); err != nil {
		return "", "", time.Time{}, err
	}
	return jti, newRT, exp, nil
}

func (s *Storage) TokenRequestByRefreshToken(ctx context.Context, token string) (op.RefreshTokenRequest, error) {
	rec, err := s.backend.GetRefreshToken(ctx, token)
	if err != nil {
		return nil, op.ErrInvalidRefreshToken
	}
	if rec.Revoked || time.Now().After(rec.ExpiresAt) {
		return nil, op.ErrInvalidRefreshToken
	}
	return &refreshTokenRequest{rec: rec, scopes: rec.Scopes}, nil
}

// TerminateSession kills BOTH token families (review P3: access-only left
// launchers holding 30-day refresh tokens silently re-minting after logout).
func (s *Storage) TerminateSession(ctx context.Context, userID string, _ string) error {
	uid := subjectUID(userID)
	err := s.backend.RevokeUserAccessTokens(ctx, uid)
	if err2 := s.backend.RevokeUserRefreshTokens(ctx, uid); err == nil {
		err = err2
	}
	return err
}

func (s *Storage) RevokeToken(ctx context.Context, tokenOrTokenID, userID, _ string) *oidc.Error {
	if userID != "" {
		// access token revocation by jti
		if err := s.backend.RevokeAccessToken(ctx, tokenOrTokenID); err != nil {
			return oidc.ErrInvalidRequest().WithParent(err)
		}
		return nil
	}
	if err := s.backend.RevokeRefreshToken(ctx, tokenOrTokenID); err != nil {
		return oidc.ErrInvalidRequest().WithParent(err)
	}
	return nil
}

func (s *Storage) GetRefreshTokenInfo(ctx context.Context, _ string, token string) (string, string, error) {
	rec, err := s.backend.GetRefreshToken(ctx, token)
	if err != nil {
		return "", "", op.ErrInvalidRefreshToken
	}
	return strconv.FormatInt(rec.UserID, 10), token, nil
}

func (s *Storage) ValidateJWTProfileScopes(_ context.Context, _ string, scopes []string) ([]string, error) {
	return scopes, nil
}

// ---- userinfo / claims ----

// SetUserinfoFromScopes is deprecated and intentionally empty.
func (s *Storage) SetUserinfoFromScopes(context.Context, *oidc.UserInfo, string, string, []string) error {
	return nil
}

func (s *Storage) SetUserinfoFromToken(ctx context.Context, userinfo *oidc.UserInfo, tokenID, subject, _ string) error {
	rec, err := s.backend.GetAccessToken(ctx, tokenID)
	if err != nil {
		return err
	}
	if !rec.RevokedAt.IsZero() {
		return errors.New("oidc: token revoked")
	}
	return s.fillUserinfo(ctx, userinfo, rec.UserID, rec.SelectedPID, rec.Scopes)
}

func (s *Storage) SetIntrospectionFromToken(ctx context.Context, resp *oidc.IntrospectionResponse, tokenID, subject, clientID string) error {
	rec, err := s.backend.GetAccessToken(ctx, tokenID)
	if err != nil {
		return err
	}
	resp.Active = rec.RevokedAt.IsZero() && time.Now().Before(rec.ExpiresAt)
	_ = subject
	_ = clientID
	return nil
}

// SetUserinfoFromRequest (op.CanSetUserinfoFromRequest) fills id_token claims.
func (s *Storage) SetUserinfoFromRequest(ctx context.Context, userinfo *oidc.UserInfo, request op.IDTokenRequest, scopes []string) error {
	pid := int64(0)
	if tr, ok := request.(op.TokenRequest); ok {
		pid = s.pidFor(tr)
	}
	return s.fillUserinfo(ctx, userinfo, subjectUID(request.GetSubject()), pid, scopes)
}

// GetPrivateClaimsFromRequest (op.CanGetPrivateClaimsFromRequest) fills
// access-token JWT claims with the same profile claims.
func (s *Storage) GetPrivateClaimsFromRequest(ctx context.Context, request op.TokenRequest, _ []string) (map[string]any, error) {
	pid := s.pidFor(request)
	return s.privateClaims(ctx, subjectUID(request.GetSubject()), pid, request.GetScopes())
}

// GetPrivateClaimsFromScopes is the non-request fallback; unused (kept for the
// op.OPStorage interface).
func (s *Storage) GetPrivateClaimsFromScopes(context.Context, string, string, []string) (map[string]any, error) {
	return nil, nil
}

func (s *Storage) privateClaims(ctx context.Context, uid, pid int64, scopes []string) (map[string]any, error) {
	players, err := s.profiles.Players(ctx, uid)
	if err != nil {
		return nil, err
	}
	avail := make([]map[string]string, 0, len(players))
	var selected map[string]string
	for _, p := range players {
		entry := map[string]string{"id": p.UUID, "name": p.Name}
		avail = append(avail, entry)
		if p.ID == pid {
			sel := map[string]string{"id": p.UUID, "name": p.Name}
			selected = sel
		}
	}
	claims := map[string]any{"availableProfiles": avail}
	if selected != nil {
		claims["selectedProfile"] = selected
	}
	return claims, nil
}

func (s *Storage) fillUserinfo(ctx context.Context, userinfo *oidc.UserInfo, uid, pid int64, scopes []string) error {
	u, err := s.profiles.User(ctx, uid)
	if err != nil {
		return err
	}
	userinfo.Subject = strconv.FormatInt(uid, 10)
	if userinfo.Claims == nil {
		userinfo.Claims = map[string]any{}
	}
	for _, sc := range scopes {
		switch sc {
		case "profile":
			userinfo.Claims["nickname"] = u.Nickname
		case "email":
			userinfo.Claims["email"] = u.Email
			userinfo.Claims["email_verified"] = u.EmailVerified
		}
	}
	extra, err := s.privateClaims(ctx, uid, pid, scopes)
	if err != nil {
		return err
	}
	for k, v := range extra {
		userinfo.Claims[k] = v
	}
	return nil
}

// ---- device flow (op.DeviceAuthorizationStorage) ----

func (s *Storage) StoreDeviceAuthorization(ctx context.Context, clientID, deviceCode, userCode string, expires time.Time, scopes []string) error {
	err := s.backend.SaveDeviceAuth(ctx, DeviceAuthRecord{
		DeviceCode: deviceCode,
		UserCode:   userCode,
		ClientID:   clientID,
		Scopes:     scopes,
		ExpiresAt:  expires,
		CreatedAt:  time.Now(),
	})
	if errors.Is(err, ErrDuplicateUserCode) {
		return op.ErrDuplicateUserCode
	}
	return err
}

func (s *Storage) GetDeviceAuthorizatonState(ctx context.Context, clientID, deviceCode string) (*op.DeviceAuthorizationState, error) {
	rec, err := s.backend.GetDeviceAuthByDeviceCode(ctx, deviceCode)
	if err != nil {
		return nil, err
	}
	if rec.ClientID != clientID {
		return nil, ErrNotFound
	}
	if rec.Done && !rec.Denied {
		// Single-use redemption (review P2: the code stayed replayable within
		// its lifetime — each poll minted a fresh access+refresh pair). The
		// approved row is consumed atomically on first successful read; any
		// later poll sees not-found and errors as expired/invalid.
		if err := s.backend.ConsumeDeviceAuth(ctx, deviceCode); err != nil {
			return nil, err
		}
	}
	state := &op.DeviceAuthorizationState{
		ClientID: rec.ClientID,
		Scopes:   rec.Scopes,
		Expires:  rec.ExpiresAt,
		Done:     rec.Done,
		Denied:   rec.Denied,
	}
	if rec.Done {
		state.Subject = strconv.FormatInt(rec.UserID, 10)
		state.AuthTime = rec.AuthTime
	}
	s.mu.Lock()
	s.devPID[state] = pidEntry{pid: rec.SelectedPID, at: time.Now()}
	s.pruneDevPIDLocked()
	s.mu.Unlock()
	return state, nil
}

func (s *Storage) pruneDevPIDLocked() {
	if len(s.devPID) < 64 {
		return
	}
	cutoff := time.Now().Add(-15 * time.Minute)
	for k, v := range s.devPID {
		if v.at.Before(cutoff) {
			delete(s.devPID, k)
		}
	}
}

// ---- helpers ----

func subjectUID(subject string) int64 {
	uid, _ := strconv.ParseInt(subject, 10, 64)
	return uid
}

// verifyClientSecret compares a bcrypt/argon2 PHC hash with the auth package
// lazily here to avoid an import cycle; wiring injects it.
var verifyClientSecret = func(hash, secret string) bool { return hash == secret }

// SetClientSecretVerifier injects the PHC verifier used for confidential
// clients (called from main with auth.VerifyPHC).
func SetClientSecretVerifier(f func(hash, secret string) bool) { verifyClientSecret = f }

// newID returns a 32-hex random id (jti / request ids).
func newID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// randomHex is used for opaque refresh tokens.
func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
