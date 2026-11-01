package ygg

import (
	"context"
	"testing"
	"time"
)

// Dual-TTL window semantics (B5.1): three-way gate per upstream
// AccessToken.php —
//
//	<3d        validate/join OK, refresh OK
//	3–7d band  validate/join 403 (暂时失效), refresh mints a replacement
//	>7d        both 403 (完全失效) — full re-login
//
// plus the rename squeeze (store-side) and fail-closed on unknown issue
// time. Kept out of http_test.go (frozen contract file).

type ttlFixture struct {
	svc  *Service
	st   *memStore
	now  time.Time
	base time.Time
}

func newTTLFixture(t *testing.T) *ttlFixture {
	t.Helper()
	st := newMemStore()
	signer, err := NewLocalSigner(t.TempDir() + "/key.pem")
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	svc := NewService(st, signer)
	f := &ttlFixture{svc: svc, st: st, base: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	svc.now = func() time.Time { return f.now }
	f.now = f.base
	return f
}

// issue seeds a token as if it had been minted `age` ago with the default
// soft window fixed at issue (Passport exp parity).
func (f *ttlFixture) issue(t *testing.T, key string, age time.Duration) Token {
	t.Helper()
	tok := Token{
		AccessToken: key, ClientToken: "ct", UserID: 1,
		CreatedAt: f.base.Add(-age),
		ExpiresAt: f.base.Add(-age).Add(f.svc.windowsFor(context.Background()).Soft),
	}
	if err := f.st.SaveToken(context.Background(), tok); err != nil {
		t.Fatalf("seed token: %v", err)
	}
	return tok
}

func TestDualTTLThreeWayGate(t *testing.T) {
	f := newTTLFixture(t)
	ctx := context.Background()

	young := f.issue(t, "young", 24*time.Hour)
	stale := f.issue(t, "stale", 96*time.Hour) // 4d: soft (3d) gone, hard (7d) alive
	dead := f.issue(t, "dead", 8*24*time.Hour) // 8d: past both

	// validate: only the young token passes
	if err := f.svc.Validate(ctx, young.AccessToken, "ct"); err != nil {
		t.Fatalf("validate young: %v", err)
	}
	if err := f.svc.Validate(ctx, stale.AccessToken, "ct"); err == nil {
		t.Fatal("validate must reject soft-expired (3-7d band)")
	}
	if err := f.svc.Validate(ctx, dead.AccessToken, "ct"); err == nil {
		t.Fatal("validate must reject fully-expired")
	}

	// refresh: young AND stale may mint replacements; dead may not
	if _, _, err := f.svc.Refresh(ctx, stale.AccessToken, "ct", 0); err != nil {
		t.Fatalf("refresh must survive the 3-7d band: %v", err)
	}
	if _, _, err := f.svc.Refresh(ctx, dead.AccessToken, "ct", 0); err == nil {
		t.Fatal("refresh must reject fully-expired (>7d)")
	}
}

func TestDualTTLRefreshMintsFullWindow(t *testing.T) {
	f := newTTLFixture(t)
	ctx := context.Background()
	stale := f.issue(t, "stale", 96*time.Hour)

	nt, _, err := f.svc.Refresh(ctx, stale.AccessToken, "ct", 0)
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	// the replacement gets a fresh soft window from NOW, not from the old
	// token's issue moment (it is a brand-new Passport token upstream)
	want := f.now.Add(f.svc.windowsFor(ctx).Soft)
	if !nt.ExpiresAt.Equal(want) {
		t.Fatalf("new ExpiresAt = %v, want %v", nt.ExpiresAt, want)
	}
	if err := f.svc.Validate(ctx, nt.AccessToken, "ct"); err != nil {
		t.Fatalf("replacement must validate: %v", err)
	}
}

func TestDualTTLInvalidatedAlwaysDead(t *testing.T) {
	f := newTTLFixture(t)
	ctx := context.Background()
	tok := f.issue(t, "killed", time.Hour)
	k := tok
	k.Invalidated = true
	f.st.tokens["killed"] = k

	if _, _, err := f.svc.Refresh(ctx, "killed", "ct", 0); err == nil {
		t.Fatal("refresh must reject invalidated tokens")
	}
	if err := f.svc.Validate(ctx, "killed", "ct"); err == nil {
		t.Fatal("validate must reject invalidated tokens")
	}
}

func TestDualTTLZeroCreatedAtFailsClosed(t *testing.T) {
	f := newTTLFixture(t)
	ctx := context.Background()
	// legacy/derived tokens may carry no issue time: once soft-expired
	// there is no recovery window to compute — fail closed.
	tok := Token{AccessToken: "nocreate", ClientToken: "ct", UserID: 1,
		ExpiresAt: f.now.Add(-time.Hour)}
	f.st.tokens["nocreate"] = tok

	if _, _, err := f.svc.Refresh(ctx, "nocreate", "ct", 0); err == nil {
		t.Fatal("zero CreatedAt + soft-expired must fail closed on refresh")
	}
}

func TestDualTTLResolverOverridesDefaults(t *testing.T) {
	f := newTTLFixture(t)
	ctx := context.Background()
	f.svc.SetTokenWindowsResolver(func(context.Context) TokenWindows {
		return TokenWindows{Soft: time.Hour, Hard: 2 * time.Hour}
	})
	// 90m old: inside default 3d soft window but PAST the resolved 1h soft
	tok := Token{AccessToken: "tight", ClientToken: "ct", UserID: 1,
		CreatedAt: f.now.Add(-90 * time.Minute),
		ExpiresAt: f.now.Add(-30 * time.Minute), // soft fixed at issue (1h)
	}
	f.st.tokens["tight"] = tok

	if err := f.svc.Validate(ctx, "tight", "ct"); err == nil {
		t.Fatal("resolved soft window must gate validate")
	}
	if _, _, err := f.svc.Refresh(ctx, "tight", "ct", 0); err != nil {
		t.Fatalf("resolved hard window (2h) must still allow refresh: %v", err)
	}
}

// TestRenameSqueezeSemantics documents the store-side rename trick: the
// rename UPDATE squeezes expires_at to the rename moment (暂时失效), so a
// pre-rename token dies at validate but keeps its refresh recovery window —
// exactly upstream's "validate/join reject, refresh survives" behavior. The
// squeeze itself is integration-tested in store; this locks the service
// half of the contract.
func TestRenameSqueezeSemantics(t *testing.T) {
	f := newTTLFixture(t)
	ctx := context.Background()
	tok := f.issue(t, "renamed", time.Hour)
	// simulate the store UPDATE: squeeze expires_at to "the rename moment"
	squeezed := tok
	squeezed.ExpiresAt = f.now.Add(-time.Second)
	f.st.tokens["renamed"] = squeezed

	if err := f.svc.Validate(ctx, "renamed", "ct"); err == nil {
		t.Fatal("pre-rename token must fail validate after the squeeze")
	}
	if _, _, err := f.svc.Refresh(ctx, "renamed", "ct", 0); err != nil {
		t.Fatalf("pre-rename token must keep its refresh recovery: %v", err)
	}
}

// ---- B5.2: ygg_rate_limit same-username gap ----

func TestAuthGapSameUsernameOnly(t *testing.T) {
	f := newTTLFixture(t)
	ctx := context.Background()
	f.svc.SetAuthGapResolver(func(context.Context) time.Duration { return time.Second })

	if ok, _ := f.svc.AuthGapAllow(ctx, "steve"); !ok {
		t.Fatal("first attempt must pass")
	}
	if ok, wait := f.svc.AuthGapAllow(ctx, "steve"); ok || wait <= 0 {
		t.Fatalf("second immediate attempt must be gated (ok=%v wait=%v)", ok, wait)
	}
	// a DIFFERENT username is unaffected (shared NAT: many users share an IP
	// but not a username — upstream keys the throttle on username)
	if ok, _ := f.svc.AuthGapAllow(ctx, "alex"); !ok {
		t.Fatal("different username must not be gated")
	}
	// after the gap elapses the original username proceeds
	f.now = f.now.Add(1100 * time.Millisecond)
	if ok, _ := f.svc.AuthGapAllow(ctx, "steve"); !ok {
		t.Fatal("gap elapsed must pass")
	}
}

func TestAuthGapDisabledByZero(t *testing.T) {
	f := newTTLFixture(t)
	ctx := context.Background()
	f.svc.SetAuthGapResolver(func(context.Context) time.Duration { return 0 })
	for i := 0; i < 5; i++ {
		if ok, _ := f.svc.AuthGapAllow(ctx, "steve"); !ok {
			t.Fatal("gap=0 must disable the throttle")
		}
	}
}
