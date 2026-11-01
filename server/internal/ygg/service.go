package ygg

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"skin-nexus/server/internal/plugin"
	"skin-nexus/server/internal/ratelimit"
)

// Errors map to the legacy response semantics (docs/phase1-dev-plan.md §1.3).
var (
	ErrBadCredentials  = errors.New("ygg: invalid credentials") // 403 ForbiddenOperationException
	ErrTokenInvalid    = errors.New("ygg: invalid token")       // 403
	ErrProfileNotFound = errors.New("ygg: profile not found")   // 204
	ErrSessionNotFound = errors.New("ygg: session not found")   // 204
	ErrEmailUnverified = errors.New("ygg: email not verified")  // 403 (join gate, fork behavior)
	ErrPlayerMismatch  = errors.New("ygg: player mismatch")     // 403 player.not-match (live-captured legacy wording)
)

// ---- storage ports (DB-backed impls land in M1; memory impls in tests) ----

// User is the auth-side user record.
type User struct {
	ID            int64
	Nickname      string
	EmailVerified bool
	CanJoinServer bool
	CreatedAt     time.Time
}

// Player is one profile (character) owned by a user.
type Player struct {
	ID        int64
	UID       int64
	Name      string
	UUID      string // 32 lowercase hex
	SkinURL   string
	SkinModel string
	CapeURL   string
	// SkinHash/CapeHash carry the raw texture hashes (pre-URL-rewrite) so
	// plugin URL filters can key on the content address. Not serialized.
	SkinHash string `json:"-"`
	CapeHash string `json:"-"`
}

// Token is an issued access token (opaque string; format intentionally
// differs from legacy Passport JWT — all tokens die at cutover).
type Token struct {
	AccessToken string
	ClientToken string
	UserID      int64
	SelectedPID int64 // 0 = none selected
	CreatedAt   time.Time
	ExpiresAt   time.Time
	Invalidated bool
}

// JoinSession is a join/hasJoined session (TTL 120s, legacy behavior).
type JoinSession struct {
	AccessToken string
	UUID        string
	ServerID    string
	ServerIP    string
	CreatedAt   time.Time
	ExpiresAt   time.Time
}

type Store interface {
	UserByID(ctx context.Context, id int64) (User, error)
	UserByIdentifier(ctx context.Context, ident string) (User, error) // email or nickname
	VerifyPassword(ctx context.Context, userID int64, password string) (bool, error)
	PlayersByUID(ctx context.Context, uid int64) ([]Player, error)
	PlayerByUUID(ctx context.Context, uuid string) (Player, error)
	PlayerByName(ctx context.Context, name string) (Player, error)
	SaveToken(ctx context.Context, t Token) error
	TokenByAccess(ctx context.Context, access string) (Token, error)
	InvalidateToken(ctx context.Context, access string) error
	InvalidateUserTokens(ctx context.Context, uid int64) error
	SaveJoin(ctx context.Context, j JoinSession) error
	JoinByTokenServer(ctx context.Context, access, serverID string) (JoinSession, error)
}

// Service implements Yggdrasil session logic. Join-session TTL follows the
// legacy deployment: 120s (not the 30s spec suggestion — see §1.7).
type Service struct {
	store   Store
	signer  Signer
	joinTTL time.Duration
	now     func() time.Time
	// resolve resolves an access token string to a Token. Default: opaque
	// store lookup. Phase-2 dual-state: the OIDC layer injects a resolver
	// that also accepts signed JWTs (phase-2 dual-state).
	resolve func(ctx context.Context, access string) (Token, error)
	// RequireVerification gates join on email_verified (fork's
	// require_verification setting). Default off: without a verification
	// mail flow the gate is a dead end for every new user; the P1 mail flow
	// ships the site setting to turn it on.
	RequireVerification bool
	// RequireVerificationResolver, when set, resolves the gate per request —
	// admin options panel writes the `require_verification` option and it
	// applies without restart (nil = static RequireVerification field).
	RequireVerificationResolver func(ctx context.Context) bool
	// VerifyGrace is how long a freshly registered account may join without
	// verifying, and VerifyGraceResolver reads `email_verify_grace_days`
	// per request (nil = static field). Without a grace window turning the
	// gate on locks every account that was never asked to verify.
	VerifyGrace         time.Duration
	VerifyGraceResolver func(ctx context.Context) time.Duration
	// Limiter protects authenticate/refresh from credential guessing
	// (nil = off; main wires settings-panel thresholds).
	Limiter *ratelimit.Limiter
	// windows holds the static dual-TTL defaults; windowsResolver, when set,
	// resolves them per request (admin panel writes ygg_token_expire_1/2 and
	// they apply without restart — RequireVerificationResolver pattern).
	windows         TokenWindows
	windowsResolver func(ctx context.Context) TokenWindows
	// B5.2 live knobs (same resolver pattern): ygg_rate_limit gap and
	// ygg_tokens_limit cap.
	gap                gapGuard
	authGapResolver    func(ctx context.Context) time.Duration
	tokenLimitResolver func(ctx context.Context) int
	searchMaxResolver  func(ctx context.Context) int

	// Plugin host hooks (v0.3.0). Nil = no plugins; all fail-open so a
	// crashing plugin can never alter the frozen Yggdrasil wire.
	// TextureURL rewrites a texture URL (content-address key + type) before
	// it is embedded in a signed profile; ProfileFilter mutates the resolved
	// texture URL map pre-signature; AuthPhase observes join/hasJoined;
	// UserLifecycle observes launcher logins.
	TextureURL    func(ctx context.Context, hash, url, typ string) string
	ProfileFilter func(ctx context.Context, p *plugin.UserProfileFilter)
	AuthPhase     func(ctx context.Context, e plugin.AuthPhaseEvent)
	UserLifecycle func(ctx context.Context, e plugin.UserLifecycleEvent)
	// JoinGuard is the v2 fail-closed guard seam (nil = no plugins). It runs
	// after all token/ownership checks and before SaveJoin.
	JoinGuard func(ctx context.Context, kind string, p plugin.EventPayload) error
}

// SetAuthGapResolver wires ygg_rate_limit (milliseconds → gap).
func (s *Service) SetAuthGapResolver(f func(ctx context.Context) time.Duration) {
	s.authGapResolver = f
}

// SetTokenLimitResolver wires ygg_tokens_limit (0 = unlimited).
func (s *Service) SetTokenLimitResolver(f func(ctx context.Context) int) {
	s.tokenLimitResolver = f
}

// SearchProfileMax is the bulk profile lookup cap (ygg_search_profile_max,
// upstream ProfileController.php:66-68: array_unique FIRST, then reject
// with ForbiddenOperationException when over — no truncation). Default 5
// matches callbacks.php:98.
func (s *Service) SearchProfileMax(ctx context.Context) int {
	max := 5
	if s.searchMaxResolver != nil {
		max = s.searchMaxResolver(ctx)
	}
	if max <= 0 {
		max = 5
	}
	return max
}

// SetSearchMaxResolver wires ygg_search_profile_max (clamped 1-100 upstream
// of here by the settings validator).
func (s *Service) SetSearchMaxResolver(f func(ctx context.Context) int) {
	s.searchMaxResolver = f
}

// windowsFor is the dual-TTL window's single decision point.
func (s *Service) windowsFor(ctx context.Context) TokenWindows {
	if s.windowsResolver != nil {
		return s.windowsResolver(ctx)
	}
	return s.windows
}

// SetTokenWindowsResolver wires the live option-backed windows (main.go:
// ygg_token_expire_1/2 from the options table).
func (s *Service) SetTokenWindowsResolver(f func(ctx context.Context) TokenWindows) {
	s.windowsResolver = f
}

// ---- B5.2: ygg_rate_limit (same-username gap) + ygg_tokens_limit ----

// gapGuard enforces ygg_rate_limit: upstream Throttle.php:15 keeps a
// per-username minimum interval (ms) between authenticate/signout calls,
// keyed by the REQUEST username. Same-username only — shared-IP users
// (the shared-NAT use case!) are unaffected, unlike a per-IP window.
type gapGuard struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// allow reports whether the key may proceed now; retryAfter is the wait.
// Stale entries (older than gap*10 or 1h) are swept on write so the map
// cannot grow unbounded (upstream's Cache entry TTLs at 3600s).
func (g *gapGuard) allow(key string, now time.Time, gap time.Duration) (bool, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.last == nil {
		g.last = map[string]time.Time{}
	}
	if len(g.last) > 4096 {
		for k, t := range g.last {
			if now.Sub(t) > gap*10 || now.Sub(t) > time.Hour {
				delete(g.last, k)
			}
		}
	}
	prev, ok := g.last[key]
	if ok {
		if wait := gap - now.Sub(prev); wait > 0 {
			return false, wait
		}
	}
	g.last[key] = now
	return true, 0
}

// AuthGapAllow is the HTTP layer's throttle entry point (authenticate and
// signout handlers). gap<=0 disables. Upstream throws
// ForbiddenOperationException on the throttle hit; the signout contract is
// "always 204", so that handler DROPS the work instead of erroring.
func (s *Service) AuthGapAllow(ctx context.Context, username string) (bool, time.Duration) {
	gap := time.Second // upstream default ygg_rate_limit = 1000ms
	if s.authGapResolver != nil {
		gap = s.authGapResolver(ctx)
	}
	if gap <= 0 {
		return true, 0
	}
	return s.gap.allow(username, s.now(), gap)
}

// TokenPruner is an OPTIONAL store capability (ygg.Store is frozen): after
// minting, the service asks the store to keep at most `keep` live tokens
// per user, LRU-style. Mysql implements it; the contract-test memStore
// does not and is skipped.
type TokenPruner interface {
	PruneUserTokens(ctx context.Context, uid int64, keep int) (evicted int64, err error)
}

// enforceTokenLimit applies ygg_tokens_limit (default 10, 0 = unlimited) —
// LRU eviction of the oldest tokens with an audit row (2026-10-24 拍板).
// Best-effort: an eviction failure must never fail the login.
func (s *Service) enforceTokenLimit(ctx context.Context, uid int64) {
	limit := 10
	if s.tokenLimitResolver != nil {
		limit = s.tokenLimitResolver(ctx)
	}
	if limit <= 0 {
		return
	}
	if p, ok := s.store.(TokenPruner); ok {
		_, _ = p.PruneUserTokens(ctx, uid, limit)
	}
}

// requireVerification is the gate's single decision point.
func (s *Service) requireVerification(ctx context.Context) bool {
	if s.RequireVerificationResolver != nil {
		return s.RequireVerificationResolver(ctx)
	}
	return s.RequireVerification
}

func (s *Service) verifyGrace(ctx context.Context) time.Duration {
	if s.VerifyGraceResolver != nil {
		return s.VerifyGraceResolver(ctx)
	}
	return s.VerifyGrace
}

// verificationBlocks is the whole email gate: option on, address unverified,
// grace window spent. Zero CreatedAt (a record built by a caller that never
// looked at the column) counts as "no grace", so the gate fails closed.
func (s *Service) verificationBlocks(ctx context.Context, u User) bool {
	if !s.requireVerification(ctx) || u.EmailVerified {
		return false
	}
	g := s.verifyGrace(ctx)
	if g > 0 && !u.CreatedAt.IsZero() && s.now().Sub(u.CreatedAt) < g {
		return false
	}
	return true
}

const (
	DefaultJoinTTL  = 120 * time.Second
	DefaultTokenTTL = 30 * 24 * time.Hour
)

// TokenWindows is the dual-TTL model (B5.1, blessing yggdrasil-connect
// parity — evidence analysis/refs-token-blacklist.md §1):
//
//	Soft (ygg_token_expire_1, default 3d): written INTO ExpiresAt at issue
//	  time (Passport::personalAccessTokensExpireIn parity). Past it the
//	  token is 暂时失效 — validate/join/userinfo reject, but refresh may
//	  still mint a replacement (launchers re-auth silently).
//	Hard (ygg_token_expire_2, default 7d): the refresh gate, derived from
//	  CreatedAt + the LIVE option. Past BOTH bounds the token is 完全失效 —
//	  refresh rejects too and a full re-login is required.
//
// Exactly mirrors upstream AccessToken.php: isExpired1 (expires_at past) /
// isExpired2 (isExpired1 && created_at+expire_2 past); isRefreshable
// consults isExpired2 only. Zero schema: both bounds live in existing
// ygg_tokens columns.
type TokenWindows struct {
	Soft time.Duration
	Hard time.Duration
}

// DefaultTokenWindows mirrors blessing callbacks.php:93-94
// (ygg_token_expire_1 = 259200s, ygg_token_expire_2 = 604800s).
var DefaultTokenWindows = TokenWindows{
	Soft: 72 * time.Hour,
	Hard: 7 * 24 * time.Hour,
}

func NewService(store Store, signer Signer) *Service {
	return &Service{
		store:   store,
		signer:  signer,
		joinTTL: DefaultJoinTTL,
		windows: DefaultTokenWindows,
		now:     time.Now,
		resolve: store.TokenByAccess,
	}
}

// refreshable mirrors upstream isRefreshable (AccessToken.php:164): a token
// is refreshable UNLESS it is fully expired (isExpired2 = isExpired1 AND
// created_at+expire_2 past). So the 3–7d band — soft-expired but inside the
// hard window — may still mint a replacement; past both, re-login.
func (s *Service) refreshable(ctx context.Context, t Token) bool {
	if t.Invalidated {
		return false
	}
	now := s.now()
	if !now.After(t.ExpiresAt) {
		return true // inside the soft window (expire_1 fixed at issue)
	}
	// soft-expired: the recovery window closes at created_at + expire_2
	if t.CreatedAt.IsZero() {
		return false // fail closed: unknown issue time = no recovery window
	}
	return !now.After(t.CreatedAt.Add(s.windowsFor(ctx).Hard))
}

// SetTokenResolver swaps the access-token resolution (dual-state hook).
func (s *Service) SetTokenResolver(f func(ctx context.Context, access string) (Token, error)) {
	s.resolve = f
}

// ResolveToken resolves an access token via the active resolver.
func (s *Service) ResolveToken(ctx context.Context, access string) (Token, error) {
	return s.resolve(ctx, access)
}

// Authenticate issues a token pair (authserver/authenticate).
func (s *Service) Authenticate(ctx context.Context, username, password, clientToken string, withProfiles bool) (Token, []Player, error) {
	u, err := s.store.UserByIdentifier(ctx, username)
	autoPID := int64(0)
	if err != nil {
		// legacy non_email_login (metadata advertises this): the identifier
		// may be a PLAYER name — reverse-lookup the owning user and
		// auto-bind that profile (CheckLoginCredentials.php parity: the
		// response then carries selectedProfile and only that profile).
		p, perr := s.store.PlayerByName(ctx, username)
		if perr != nil {
			return Token{}, nil, ErrBadCredentials
		}
		u, err = s.store.UserByID(ctx, p.UID)
		if err != nil {
			return Token{}, nil, ErrBadCredentials
		}
		autoPID = p.ID
	}
	// Legacy CheckLoginCredentials gates at authenticate, not only at join:
	// banned users and (gate on) unverified users are rejected at login.
	if s.verificationBlocks(ctx, u) {
		return Token{}, nil, ErrEmailUnverified
	}
	if !u.CanJoinServer {
		return Token{}, nil, ErrBadCredentials
	}
	ok, err := s.store.VerifyPassword(ctx, u.ID, password)
	if err != nil {
		// Frozen wire keeps the 403 body, but a verify/db fault must not be
		// indistinguishable from bad credentials in ops logs (review P2).
		log.Printf("ygg authenticate: verify uid=%d: %v", u.ID, err)
		return Token{}, nil, ErrBadCredentials
	}
	if !ok {
		return Token{}, nil, ErrBadCredentials
	}
	if clientToken == "" {
		clientToken = randomHex(16)
	}
	players, _ := s.store.PlayersByUID(ctx, u.ID)
	if autoPID != 0 {
		// player-name login: availableProfiles collapses to that profile
		for _, pl := range players {
			if pl.ID == autoPID {
				players = []Player{pl}
				break
			}
		}
	}
	t := Token{
		AccessToken: randomHex(32),
		ClientToken: clientToken,
		UserID:      u.ID,
		CreatedAt:   s.now(),
		ExpiresAt:   s.now().Add(s.windowsFor(ctx).Soft), // expire_1 fixed at issue (Passport exp parity)
	}
	// Legacy parity (analysis/authlib-injector.md:221, yggdrasil-connect.md
	// :380): a single profile is auto-bound to the token; multiple profiles
	// leave the choice to the client (refresh with selectedProfile).
	if len(players) == 1 {
		t.SelectedPID = players[0].ID
	}
	if err := s.store.SaveToken(ctx, t); err != nil {
		return Token{}, nil, err
	}
	s.enforceTokenLimit(ctx, t.UserID) // ygg_tokens_limit LRU (best-effort)
	if s.UserLifecycle != nil {
		s.UserLifecycle(ctx, plugin.UserLifecycleEvent{Kind: "logged_in", UserID: u.ID})
	}
	return t, players, nil
}

// Refresh re-issues an access token (authserver/refresh). Uses the opaque
// store lookup (not the dual-state resolver) by design: legacy refresh takes
// opaque access tokens; OIDC JWTs refresh via /oauth/token (refresh_token
// grant) instead.
func (s *Service) Refresh(ctx context.Context, accessToken, clientToken string, selectPID int64) (Token, []Player, error) {
	old, err := s.store.TokenByAccess(ctx, accessToken)
	if err != nil || !s.refreshable(ctx, old) {
		return Token{}, nil, ErrTokenInvalid
	}
	if clientToken != "" && clientToken != old.ClientToken {
		return Token{}, nil, ErrTokenInvalid
	}
	// Ownership gate (review P2: a token holder could select ANY player UUID
	// and inherit its pid — foreign-profile binding + existence oracle).
	if selectPID != 0 {
		mine, err := s.store.PlayersByUID(ctx, old.UserID)
		if err != nil {
			return Token{}, nil, ErrTokenInvalid
		}
		owned := false
		for _, pl := range mine {
			if pl.ID == selectPID {
				owned = true
				break
			}
		}
		if !owned {
			return Token{}, nil, ErrPlayerMismatch
		}
	}
	// Legacy semantics: a refresh invalidates the previous token. (Validation
	// happens first so a rejected select never burns the caller's token.)
	_ = s.store.InvalidateToken(ctx, accessToken)
	t := Token{
		AccessToken: randomHex(32),
		ClientToken: old.ClientToken,
		UserID:      old.UserID,
		SelectedPID: selectPID,
		CreatedAt:   s.now(),
		ExpiresAt:   s.now().Add(s.windowsFor(ctx).Soft), // expire_1 fixed at issue (Passport exp parity)
	}
	if t.SelectedPID == 0 {
		t.SelectedPID = old.SelectedPID
	}
	if err := s.store.SaveToken(ctx, t); err != nil {
		return Token{}, nil, err
	}
	s.enforceTokenLimit(ctx, t.UserID) // ygg_tokens_limit LRU (best-effort)
	players, _ := s.store.PlayersByUID(ctx, t.UserID)
	return t, players, nil
}

// Validate reports whether a token is currently usable (204 vs 403).
// Dual-state: accepts both opaque tokens and OIDC JWTs (SJMCL validates the
// OAuth access token via this endpoint). JWT-resolved tokens carry no
// clientToken, so the mismatch check is skipped for them.
func (s *Service) Validate(ctx context.Context, accessToken, clientToken string) error {
	t, err := s.resolve(ctx, accessToken)
	if err != nil || t.Invalidated || s.now().After(t.ExpiresAt) {
		return ErrTokenInvalid
	}
	if clientToken != "" && t.ClientToken != "" && clientToken != t.ClientToken {
		return ErrTokenInvalid
	}
	return nil
}

// Invalidate kills one token (always 204 at the HTTP layer).
func (s *Service) Invalidate(ctx context.Context, accessToken string) {
	_ = s.store.InvalidateToken(ctx, accessToken)
}

// SignOut kills all tokens of a user after password check (always 204).
func (s *Service) SignOut(ctx context.Context, username, password string) {
	u, err := s.store.UserByIdentifier(ctx, username)
	if err != nil {
		return
	}
	if ok, _ := s.store.VerifyPassword(ctx, u.ID, password); ok {
		_ = s.store.InvalidateUserTokens(ctx, u.ID)
	}
}

// Join records a join session (sessionserver/join, 204 at HTTP layer).
// Fork behavior preserved: unverified email may not join (ErrEmailUnverified).
// Dual-state: the access token may be an OIDC JWT (authlib-injector joins with
// the OAuth access token). The player must belong to the token owner and match
// the token's selected profile (legacy player.not-match semantics).
func (s *Service) Join(ctx context.Context, accessToken, playerUUID, serverID, serverIP string) error {
	t, err := s.resolve(ctx, accessToken)
	if err != nil || t.Invalidated || s.now().After(t.ExpiresAt) {
		return ErrTokenInvalid
	}
	u, err := s.store.UserByID(ctx, t.UserID)
	if err != nil {
		return ErrTokenInvalid
	}
	if s.verificationBlocks(ctx, u) {
		return ErrEmailUnverified
	}
	if !u.CanJoinServer {
		return ErrTokenInvalid
	}
	p, err := s.store.PlayerByUUID(ctx, NormalizeUUID(playerUUID))
	if err != nil || p.UID != t.UserID {
		return ErrPlayerMismatch // legacy: player.not-match / not owned
	}
	if t.SelectedPID != 0 && p.ID != t.SelectedPID {
		return ErrPlayerMismatch
	}
	if s.JoinGuard != nil {
		if err := s.JoinGuard(ctx, "auth.join.check", plugin.EventPayload{
			UserID: t.UserID, PlayerID: p.ID, PlayerName: p.Name, UUID: NormalizeUUID(playerUUID),
			Data: map[string]any{"serverId": serverID, "serverIP": serverIP},
		}); err != nil {
			return &JoinDeniedError{Message: err.Error()}
		}
	}
	if s.AuthPhase != nil {
		s.AuthPhase(ctx, plugin.AuthPhaseEvent{Phase: "pre_join", UUID: NormalizeUUID(playerUUID), Player: p.Name})
	}
	return s.store.SaveJoin(ctx, JoinSession{
		AccessToken: t.AccessToken,
		UUID:        NormalizeUUID(playerUUID),
		ServerID:    serverID,
		ServerIP:    serverIP,
		CreatedAt:   s.now(),
		ExpiresAt:   s.now().Add(s.joinTTL),
	})
}

// sameIP compares two IP spellings by value (chain-trace P2: string compare
// killed IPv6 players — Go's net.IP text vs Java InetAddress.getHostAddress()
// normalize the same address differently). Falls back to exact string match
// when either side does not parse.
func sameIP(a, b string) bool {
	if ia, ib := net.ParseIP(a), net.ParseIP(b); ia != nil && ib != nil {
		return ia.Equal(ib)
	}
	return a == b
}

// NormalizeUUID maps the UUID wire formats seen in the wild (bare 32-hex and
// hyphenated 8-4-4-4-12, any case) onto the canonical lowercase 32-hex form
// used in storage — both spellings must be tolerated (live capture 2026-10-23).
func NormalizeUUID(u string) string {
	return strings.ToLower(strings.ReplaceAll(u, "-", ""))
}

// PlayerPIDByUUID resolves a profile UUID (any wire format) to its row id;
// used by refresh to honour the client's selectedProfile.
func (s *Service) PlayerPIDByUUID(ctx context.Context, uuid string) (int64, error) {
	p, err := s.store.PlayerByUUID(ctx, NormalizeUUID(uuid))
	if err != nil {
		return 0, err
	}
	return p.ID, nil
}

// HasJoined resolves a join session to a signed profile.
// Returns ErrSessionNotFound (HTTP 204) on: unknown session, expired,
// serverId mismatch, or IP mismatch (fork behavior).
func (s *Service) HasJoined(ctx context.Context, username, serverID, serverIP string) (Profile, error) {
	// legacy: missing parameters answer 204 directly (chain-trace P2: an
	// empty serverId used to match ANY live join and return a signed profile)
	if username == "" || serverID == "" {
		return Profile{}, ErrSessionNotFound
	}
	p, err := s.store.PlayerByName(ctx, username)
	if err != nil {
		return Profile{}, ErrProfileNotFound
	}
	// Find the most recent live join session for this player.
	// (Store scan is O(sessions); acceptable — TTL 120s bounds the table.)
	js, err := s.findLiveJoin(ctx, p, serverID, serverIP)
	if err != nil {
		return Profile{}, err
	}
	_ = js
	if s.AuthPhase != nil {
		s.AuthPhase(ctx, plugin.AuthPhaseEvent{Phase: "post_has_joined", UUID: p.UUID, Player: p.Name})
	}
	return s.buildProfile(ctx, p, true) // hasJoined always signs
}

func (s *Service) findLiveJoin(ctx context.Context, p Player, serverID, serverIP string) (JoinSession, error) {
	type scanner interface {
		FindLiveJoins(ctx context.Context, uuid string, now time.Time) ([]JoinSession, error)
	}
	sc, ok := s.store.(scanner)
	if !ok {
		return JoinSession{}, ErrSessionNotFound
	}
	joins, err := sc.FindLiveJoins(ctx, p.UUID, s.now())
	if err != nil {
		return JoinSession{}, ErrSessionNotFound
	}
	for _, j := range joins {
		if serverID != "" && j.ServerID != serverID {
			continue
		}
		if serverIP != "" && j.ServerIP != "" && !sameIP(j.ServerIP, serverIP) {
			continue // fork: IP mismatch -> 204
		}
		return j, nil
	}
	return JoinSession{}, ErrSessionNotFound
}

// ProfileByUUID serves sessionserver/profile/{uuid}.
// unsigned=false triggers property signatures; missing profile = ErrProfileNotFound (204).
func (s *Service) ProfileByUUID(ctx context.Context, uuid string, withSig bool) (Profile, error) {
	p, err := s.store.PlayerByUUID(ctx, NormalizeUUID(uuid))
	if err != nil {
		return Profile{}, ErrProfileNotFound
	}
	return s.buildProfile(ctx, p, withSig)
}

// LookupByName serves api/users/profiles/minecraft/{name} (no properties).
func (s *Service) LookupByName(ctx context.Context, name string) (Player, error) {
	return s.store.PlayerByName(ctx, name)
}

// LookupBulk serves api/profiles/minecraft (no properties).
func (s *Service) LookupBulk(ctx context.Context, names []string) []Player {
	out := make([]Player, 0, len(names))
	for _, n := range names {
		if p, err := s.store.PlayerByName(ctx, n); err == nil {
			out = append(out, p)
		}
	}
	return out
}

func (s *Service) buildProfile(ctx context.Context, p Player, withSig bool) (Profile, error) {
	ts := TextureSet{SkinURL: p.SkinURL, SkinModel: p.SkinModel, CapeURL: p.CapeURL}
	// Plugin filters run strictly BEFORE AssembleTextures: the signature
	// covers whatever URLs end up in the textures property, so rewriting
	// after signing would make clients reject the profile.
	if s.TextureURL != nil {
		if ts.SkinURL != "" {
			ts.SkinURL = s.TextureURL(ctx, p.SkinHash, ts.SkinURL, "skin")
		}
		if ts.CapeURL != "" {
			ts.CapeURL = s.TextureURL(ctx, p.CapeHash, ts.CapeURL, "cape")
		}
	}
	if s.ProfileFilter != nil {
		pf := plugin.UserProfileFilter{
			PlayerID:    p.ID,
			PlayerName:  p.Name,
			UUID:        p.UUID,
			TexturesURL: map[string]string{"SKIN": ts.SkinURL, "CAPE": ts.CapeURL},
		}
		s.ProfileFilter(ctx, &pf)
		ts.SkinURL = pf.TexturesURL["SKIN"]
		ts.CapeURL = pf.TexturesURL["CAPE"]
	}
	props, err := AssembleTextures(p.UUID, p.Name, ts, s.now(), withSig, s.signer)
	if err != nil {
		return Profile{}, err
	}
	return Profile{ID: p.UUID, Name: p.Name, Properties: props}, nil
}

// PublicKeyPEM exposes the metadata signing key.
func (s *Service) PublicKeyPEM() string { return s.signer.PublicKeyPEM() }

// SignerFingerprint exposes the active key fingerprint for admin UI/audit.
func (s *Service) SignerFingerprint() string { return s.signer.KeyFingerprint() }

func randomHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// JoinDeniedError is returned when a plugin guard denies a Yggdrasil join.
// The HTTP layer maps it to 403 ForbiddenOperationException with the plugin
// message, never to a 500.
type JoinDeniedError struct{ Message string }

func (e *JoinDeniedError) Error() string { return e.Message }
