package ygg

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"skin-nexus/server/internal/plugin"
)

// ErrStoreFault identifies an unavailable verification dependency. It is
// deliberately distinct from a session miss: callers must never turn a fault
// into an official-provider fallback.
var ErrStoreFault = errors.New("ygg: verification store fault")

// VerificationIdentity is the policy input. A nil Player explicitly means the
// username has no local account; policies must not infer a uid from the name.
type VerificationIdentity struct {
	Player   *Player
	Username string
	// OfficialUUID and OfficialName are populated only from a successful
	// official fallback. They never imply a local UID or an explicit binding.
	OfficialUUID string
	OfficialName string
}

// VerificationDecision is the complete pre-session policy result. Fallback is
// considered only after a genuine local session miss and only when allowed.
type VerificationDecision struct {
	Allow         bool
	AllowFallback bool
	ReasonCode    string
}

// VerificationPolicy is the narrow module seam. internal/ygg does not know
// about tenants, credentials or serveraccess storage.
type VerificationPolicy interface {
	Evaluate(ctx context.Context, identity VerificationIdentity) (VerificationDecision, error)
}

// VerificationFallbackPolicy optionally resolves dynamic fallback gates after a
// true local session miss. Policies without this seam use the decision's static
// AllowFallback result; serveraccess implements it so option-store faults cannot
// block a local hit.
type VerificationFallbackPolicy interface {
	ResolveFallback(ctx context.Context, identity VerificationIdentity) (bool, error)
}

// FallbackResult distinguishes a healthy upstream miss (204/4xx) from an
// upstream fault. A breaker must never open because a player legitimately has
// no official profile.
type FallbackResult struct {
	Profile       []byte
	Matched       bool
	UpstreamFault bool
}

// FallbackPermit bounds one official-provider attempt. Release receives true
// for any healthy upstream response, including an expected miss.
type FallbackPermit struct {
	Release    func(healthy bool)
	ReasonCode string
}

// VerificationFallbackGuard is optional so legacy and local verification keep
// their existing behavior. A denied permit fails closed before any outbound I/O.
type VerificationFallbackGuard interface {
	AdmitFallback() (FallbackPermit, bool)
}

// VerificationResult carries wire-neutral event facts. Profile is either a
// locally signed profile or the official response bytes, never re-signed.
type VerificationResult struct {
	Profile       []byte
	JoinSessionID int64
	Outcome       string
	ReasonCode    string
	Source        string
	// Identity carries the resolved local player to the caller's event sink.
	// It is wire-neutral and never serialized into a Yggdrasil response.
	Identity VerificationIdentity
}

// ScopedHasJoined is used only by the optional server-access verification
// routes. Legacy HasJoined and its HTTP handler remain separate and unchanged.
func (s *Service) ScopedHasJoined(ctx context.Context, username, serverID, serverIP string, policy VerificationPolicy) (VerificationResult, error) {
	result := VerificationResult{Outcome: "error", ReasonCode: "verification_unavailable", Identity: VerificationIdentity{Username: username}}
	if username == "" || serverID == "" || len(username) > 64 || len(serverID) > 128 {
		result.Outcome, result.ReasonCode = "deny", "invalid_arguments"
		return result, ErrProfileNotFound
	}
	if policy == nil {
		return result, ErrStoreFault
	}

	var player *Player
	p, err := s.store.PlayerByName(ctx, username)
	if err == nil {
		player = &p
		result.Identity.Player = player
	} else if !errors.Is(err, ErrProfileNotFound) && !errors.Is(err, sql.ErrNoRows) {
		result.Outcome, result.ReasonCode = "error", "store_fault"
		return result, fmt.Errorf("%w: player lookup: %v", ErrStoreFault, err)
	}
	if player != nil {
		permission, permissionErr := s.store.UserPermission(ctx, player.UID)
		if permissionErr != nil {
			result.Outcome, result.ReasonCode = "error", "store_fault"
			return result, fmt.Errorf("%w: permission lookup: %v", ErrStoreFault, permissionErr)
		}
		if permission == -1 {
			result.Outcome, result.ReasonCode = "deny", "platform_banned"
			return result, ErrAccessDenied
		}
	}
	decision, err := policy.Evaluate(ctx, result.Identity)
	if err != nil {
		result.Outcome, result.ReasonCode = "error", "policy_fault"
		return result, fmt.Errorf("%w: policy: %v", ErrStoreFault, err)
	}
	if !decision.Allow {
		reason := decision.ReasonCode
		if reason == "" {
			reason = "policy_denied"
		}
		result.Outcome, result.ReasonCode = "deny", reason
		return result, ErrAccessDenied
	}

	if player == nil {
		return s.scopedMiss(ctx, username, serverID, serverIP, decision, result, policy)
	}
	join, err := s.scopedLiveJoin(ctx, *player, serverID, serverIP)
	if errors.Is(err, ErrSessionNotFound) {
		return s.scopedMiss(ctx, username, serverID, serverIP, decision, result, policy)
	}
	if err != nil {
		result.Outcome, result.ReasonCode = "error", "store_fault"
		return result, fmt.Errorf("%w: join lookup: %v", ErrStoreFault, err)
	}
	if s.AuthPhase != nil {
		s.AuthPhase(ctx, plugin.AuthPhaseEvent{Phase: "post_has_joined", UUID: player.UUID, Player: player.Name})
	}
	profile, err := s.buildProfile(ctx, *player, true)
	if err != nil {
		result.Outcome, result.ReasonCode = "error", "signing_fault"
		return result, err
	}
	body, err := json.Marshal(profile)
	if err != nil {
		result.Outcome, result.ReasonCode = "error", "encoding_fault"
		return result, err
	}
	body = append(body, '\n')
	result.Profile, result.JoinSessionID, result.Outcome, result.ReasonCode, result.Source = body, join.ID, "allow", "local_session", "local"
	return result, nil
}

func (s *Service) scopedMiss(ctx context.Context, username, serverID, serverIP string, decision VerificationDecision, result VerificationResult, policy VerificationPolicy) (VerificationResult, error) {
	result.Outcome, result.ReasonCode, result.Source = "miss", "session_not_found", "none"
	if !decision.AllowFallback || s.scopedFallback == nil {
		return result, ErrSessionNotFound
	}
	if dynamic, ok := policy.(VerificationFallbackPolicy); ok {
		allowed, err := dynamic.ResolveFallback(ctx, result.Identity)
		if err != nil {
			result.Outcome, result.ReasonCode = "error", "policy_fault"
			return result, fmt.Errorf("%w: fallback policy: %v", ErrStoreFault, err)
		}
		if !allowed {
			return result, ErrSessionNotFound
		}
	}
	var permit FallbackPermit
	if guard, ok := policy.(VerificationFallbackGuard); ok {
		var allowed bool
		permit, allowed = guard.AdmitFallback()
		if !allowed {
			result.Outcome, result.ReasonCode = "deny", permit.ReasonCode
			if result.ReasonCode == "" {
				result.ReasonCode = "fallback_concurrent_limit"
			}
			return result, ErrAccessDenied
		}
	}
	attempt := callScopedFallback(s.scopedFallback, ctx, username, serverID, serverIP, permit.Release)
	if !attempt.Matched {
		if attempt.UpstreamFault {
			result.Outcome, result.ReasonCode, result.Source = "error", "fallback_unavailable", "official"
			return result, ErrSessionNotFound
		}
		return result, ErrSessionNotFound
	}
	officialUUID, officialName, ok := parseOfficialIdentity(attempt.Profile)
	if !ok {
		return result, ErrSessionNotFound
	}
	result.Identity.OfficialUUID, result.Identity.OfficialName = officialUUID, officialName
	result.Profile, result.Outcome, result.ReasonCode, result.Source = attempt.Profile, "allow", "official_fallback", "official"
	return result, nil
}

func callScopedFallback(fallback func(context.Context, string, string, string) FallbackResult, ctx context.Context, username, serverID, serverIP string, release func(bool)) (result FallbackResult) {
	completed := false
	defer func() {
		if release != nil && !completed {
			release(false)
		}
	}()
	result = fallback(ctx, username, serverID, serverIP)
	completed = true
	if release != nil {
		release(!result.UpstreamFault)
	}
	return result
}

// parseOfficialIdentity reads event facts without rewriting the official body.
// A malformed identity is a miss even if the body passed the fallback's generic
// JSON probe; no local UID is inferred from either official field.
func parseOfficialIdentity(body []byte) (uuid, name string, ok bool) {
	var profile struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &profile); err != nil || profile.Name == "" {
		return "", "", false
	}
	uuid = NormalizeUUID(profile.ID)
	if len(uuid) != 32 || !isLowerHex(uuid) {
		return "", "", false
	}
	return uuid, profile.Name, true
}

func (s *Service) scopedLiveJoin(ctx context.Context, player Player, serverID, serverIP string) (JoinSession, error) {
	scanner, ok := s.store.(interface {
		FindLiveJoins(context.Context, string, time.Time) ([]JoinSession, error)
	})
	if !ok {
		return JoinSession{}, errors.New("live join lookup unsupported")
	}
	joins, err := scanner.FindLiveJoins(ctx, player.UUID, s.now())
	if err != nil {
		return JoinSession{}, err
	}
	for _, join := range joins {
		if join.ServerID == serverID && (serverIP == "" || join.ServerIP == "" || sameIP(join.ServerIP, serverIP)) {
			return join, nil
		}
	}
	return JoinSession{}, ErrSessionNotFound
}
