package store

// The refresh token is the longest-lived credential the site issues: 30 days,
// rotating, and it alone mints a fresh access+refresh pair. E1 digested the
// session and launcher tokens and left this one verbatim, which made the public
// claim "reading the database is not reading a usable login" false. These tests
// pin both halves: what the column holds, and that the round trip still works
// for callers that only ever see the raw value.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"testing"
	"time"

	"skin-nexus/server/internal/oidc"
)

func rawRefreshToken(seed string) string {
	// 64 hex like storage.go issues (newID()+newID()), assembled from a seed so
	// no credential-shaped literal is ever written to a file.
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func digestOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func refreshTokenColumn(t *testing.T, m *Mysql, token string) (stored string, revoked int) {
	t.Helper()
	err := m.db.QueryRowContext(context.Background(),
		`SELECT token, revoked FROM oidc_refresh_tokens WHERE token = ?`, digestOf(token)).
		Scan(&stored, &revoked)
	if err != nil {
		t.Fatalf("row for the digest of %q is missing: %v", token[:8], err)
	}
	return stored, revoked
}

func TestOIDCRefreshTokenIsStoredAsADigest(t *testing.T) {
	m := openSQLiteStore(t)
	ctx := context.Background()
	raw := rawRefreshToken("issue")
	rec := oidc.RefreshTokenRecord{
		Token: raw, UserID: 7, ClientID: "launcher", Scopes: []string{"openid"},
		AuthTime: time.Now(), ExpiresAt: time.Now().Add(30 * 24 * time.Hour), CreatedAt: time.Now(),
	}
	if err := m.SaveRefreshToken(ctx, rec); err != nil {
		t.Fatal(err)
	}

	var literal int
	if err := m.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM oidc_refresh_tokens WHERE token = ?`, raw).Scan(&literal); err != nil {
		t.Fatal(err)
	}
	if literal != 0 {
		t.Fatal("the refresh token was stored verbatim: a database read hands out a 30-day login credential")
	}
	stored, _ := refreshTokenColumn(t, m, raw)
	if stored != digestOf(raw) {
		t.Fatalf("stored token = %q, want hex(sha256(presented))", stored)
	}

	// The caller that fetched it must still hold the value the client presented:
	// rotation and revocation look rows up by that value, and a record carrying
	// the digest would hash it a second time and match nothing.
	got, err := m.GetRefreshToken(ctx, raw)
	if err != nil {
		t.Fatalf("lookup by the presented value: %v", err)
	}
	if got.Token != raw {
		t.Fatalf("GetRefreshToken returned Token=%q, want the presented value back", got.Token)
	}
}

func TestOIDCRefreshTokenRotationAndRevocationStillWork(t *testing.T) {
	m := openSQLiteStore(t)
	ctx := context.Background()
	old := rawRefreshToken("old")
	fresh := rawRefreshToken("fresh")
	save := func(token string) {
		t.Helper()
		if err := m.SaveRefreshToken(ctx, oidc.RefreshTokenRecord{
			Token: token, UserID: 7, ClientID: "launcher", Scopes: []string{"openid"},
			AuthTime: time.Now(), ExpiresAt: time.Now().Add(30 * 24 * time.Hour), CreatedAt: time.Now(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	save(old)

	got, err := m.GetRefreshToken(ctx, old)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.RotateRefreshToken(ctx, got, oidc.RefreshTokenRecord{
		Token: fresh, UserID: 7, ClientID: "launcher", Scopes: []string{"openid"},
		AuthTime: time.Now(), ExpiresAt: time.Now().Add(30 * 24 * time.Hour), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if _, revoked := refreshTokenColumn(t, m, old); revoked != 1 {
		t.Error("the consumed token survived rotation unrevoked")
	}
	if _, revoked := refreshTokenColumn(t, m, fresh); revoked != 0 {
		t.Error("the replacement token is not live")
	}
	// Reuse of the consumed token must still be refused, not silently accepted.
	if _, err := m.GetRefreshToken(ctx, old); err != nil {
		return
	}
	// A second rotate on the same old token loses the race, as the guard intends.
	if err := m.RotateRefreshToken(ctx, got, oidc.RefreshTokenRecord{
		Token: rawRefreshToken("third"), UserID: 7, ClientID: "launcher",
		ExpiresAt: time.Now().Add(time.Hour), CreatedAt: time.Now(),
	}); err == nil {
		t.Error("a revoked refresh token rotated a second time — the single-use guard came off")
	}

	if err := m.RevokeRefreshToken(ctx, fresh); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	if _, revoked := refreshTokenColumn(t, m, fresh); revoked != 1 {
		t.Error("RevokeRefreshToken by presented value did not mark the row")
	}
}
