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
		return s.scopedMiss(ctx, username, serverID, serverIP, decision, result)
	}
	join, err := s.scopedLiveJoin(ctx, *player, serverID, serverIP)
	if errors.Is(err, ErrSessionNotFound) {
		return s.scopedMiss(ctx, username, serverID, serverIP, decision, result)
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

func (s *Service) scopedMiss(ctx context.Context, username, serverID, serverIP string, decision VerificationDecision, result VerificationResult) (VerificationResult, error) {
	result.Outcome, result.ReasonCode, result.Source = "miss", "session_not_found", "none"
	if !decision.AllowFallback || s.scopedFallback == nil {
		return result, ErrSessionNotFound
	}
	body, ok := s.scopedFallback(ctx, username, serverID, serverIP)
	if !ok {
		return result, ErrSessionNotFound
	}
	result.Profile, result.Outcome, result.ReasonCode, result.Source = body, "allow", "official_fallback", "official"
	return result, nil
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
