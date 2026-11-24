package ygg

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type verificationPolicyFunc func(VerificationIdentity) (VerificationDecision, error)

func (f verificationPolicyFunc) Evaluate(_ context.Context, identity VerificationIdentity) (VerificationDecision, error) {
	return f(identity)
}

type verificationStore struct {
	*memStore
	playerErr     error
	permissionErr error
	joinErr       error
}

func (s *verificationStore) PlayerByName(ctx context.Context, name string) (Player, error) {
	if s.playerErr != nil {
		return Player{}, s.playerErr
	}
	p, err := s.memStore.PlayerByName(ctx, name)
	if err != nil {
		return Player{}, ErrProfileNotFound
	}
	return p, nil
}

func (s *verificationStore) UserPermission(ctx context.Context, userID int64) (int, error) {
	if s.permissionErr != nil {
		return 0, s.permissionErr
	}
	return s.memStore.UserPermission(ctx, userID)
}

func (s *verificationStore) FindLiveJoins(ctx context.Context, uuid string, now time.Time) ([]JoinSession, error) {
	if s.joinErr != nil {
		return nil, s.joinErr
	}
	return s.memStore.FindLiveJoins(ctx, uuid, now)
}

type fallbackCounter func(*http.Request) (*http.Response, error)

func (f fallbackCounter) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func fallbackResponse(t *testing.T, body string) *http.Response {
	t.Helper()
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestScopedVerificationPolicyDenyNeverFallsBack(t *testing.T) {
	st := &verificationStore{memStore: newMemStore()}
	st.players["Alice"] = Player{ID: 1, UID: 7, Name: "Alice", UUID: "0123456789abcdef0123456789abcdef"}
	svc := NewService(st, g10Signer(t))
	api := NewAPI(svc, MetadataConfig{})
	calls := 0
	api.SetFallback(&Fallback{Enabled: func(context.Context) bool { return true }, Client: &http.Client{Transport: fallbackCounter(func(*http.Request) (*http.Response, error) {
		calls++
		return nil, errors.New("must not be called")
	})}})

	got, err := svc.ScopedHasJoined(context.Background(), "Alice", "server", "", verificationPolicyFunc(func(VerificationIdentity) (VerificationDecision, error) {
		return VerificationDecision{ReasonCode: "platform_banned"}, nil
	}))
	if !errors.Is(err, ErrAccessDenied) || got.Outcome != "deny" || got.ReasonCode != "platform_banned" {
		t.Fatalf("verification = %+v, %v", got, err)
	}
	if calls != 0 {
		t.Fatalf("fallback calls = %d, want 0", calls)
	}
}

func TestScopedVerificationDatabaseFaultNeverFallsBack(t *testing.T) {
	for _, tc := range []struct {
		name string
		st   *verificationStore
	}{
		{name: "player lookup", st: &verificationStore{memStore: newMemStore(), playerErr: errors.New("db unavailable")}},
		{name: "permission lookup", st: &verificationStore{memStore: newMemStore(), permissionErr: errors.New("db unavailable")}},
		{name: "join lookup", st: &verificationStore{memStore: newMemStore(), joinErr: errors.New("db unavailable")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name != "player lookup" {
				tc.st.players["Alice"] = Player{ID: 1, UID: 7, Name: "Alice", UUID: "0123456789abcdef0123456789abcdef"}
			}
			svc := NewService(tc.st, g10Signer(t))
			api := NewAPI(svc, MetadataConfig{})
			calls := 0
			api.SetFallback(&Fallback{Enabled: func(context.Context) bool { return true }, Client: &http.Client{Transport: fallbackCounter(func(*http.Request) (*http.Response, error) {
				calls++
				return nil, errors.New("must not be called")
			})}})
			got, err := svc.ScopedHasJoined(context.Background(), "Alice", "server", "", verificationPolicyFunc(func(VerificationIdentity) (VerificationDecision, error) {
				return VerificationDecision{Allow: true, AllowFallback: true}, nil
			}))
			if !errors.Is(err, ErrStoreFault) || got.ReasonCode != "store_fault" {
				t.Fatalf("verification = %+v, %v", got, err)
			}
			if calls != 0 {
				t.Fatalf("fallback calls = %d, want 0", calls)
			}
		})
	}
}

func TestScopedVerificationUnknownPlayerIsExplicitAndTrueMissMayFallBack(t *testing.T) {
	st := &verificationStore{memStore: newMemStore()}
	svc := NewService(st, g10Signer(t))
	api := NewAPI(svc, MetadataConfig{})
	calls := 0
	api.SetFallback(&Fallback{BaseURL: "https://official.example", Enabled: func(context.Context) bool { return true }, Client: &http.Client{Transport: fallbackCounter(func(*http.Request) (*http.Response, error) {
		calls++
		return fallbackResponse(t, `{"id":"0123456789abcdef0123456789abcdef","name":"Alice"}`), nil
	})}})
	known := false
	got, err := svc.ScopedHasJoined(context.Background(), "Alice", "server", "", verificationPolicyFunc(func(identity VerificationIdentity) (VerificationDecision, error) {
		known = identity.Player == nil
		return VerificationDecision{Allow: true, AllowFallback: true}, nil
	}))
	if err != nil || known != true || calls != 1 || got.Source != "official" || string(got.Profile) != `{"id":"0123456789abcdef0123456789abcdef","name":"Alice"}` {
		t.Fatalf("verification = %+v, %v, known=%v calls=%d", got, err, known, calls)
	}
}
