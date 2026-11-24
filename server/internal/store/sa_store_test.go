package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

var saSeedSeq atomic.Int64

func saSeedUser(t *testing.T, st *Mysql) int64 {
	t.Helper()
	n := saSeedSeq.Add(1)
	uid, err := st.CreateUser(context.Background(), fmt.Sprintf("sa%d@example.com", n), fmt.Sprintf("sa%d", n), "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	return uid
}

func saSeedUserAs(t *testing.T, st *Mysql, email, nick string) int64 {
	t.Helper()
	uid, err := st.CreateUser(context.Background(), email, nick, "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	return uid
}

func TestServerAccessStoreTenantServerAndMemberScope(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	u1 := saSeedUser(t, st)
	u2 := saSeedUser(t, st)
	t1, err := st.CreateSATenant(ctx, "one", 2)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := st.CreateSATenant(ctx, "two", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddSATenantMember(ctx, t1, u1, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSATenantMember(ctx, t2, u2, "owner"); err != nil {
		t.Fatal(err)
	}
	member, ok, err := st.SATenantMemberByUID(ctx, u1)
	if err != nil || !ok || member.TenantID != t1 || member.Role != "owner" || member.Nickname == "" {
		t.Fatalf("membership = %+v ok=%v err=%v", member, ok, err)
	}
	if _, ok, err := st.SATenantMemberByUID(ctx, 999999); err != nil || ok {
		t.Fatalf("non-member ok=%v err=%v", ok, err)
	}
	if err := st.AddSATenantMember(ctx, t1, u2, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSATenantInvite(ctx, t1, "second@example.com", "viewer", saTestHash("no-downgrade"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AcceptSATenantInvite(ctx, saTestHash("no-downgrade"), u2, "second@example.com", time.Now()); err != nil {
		t.Fatal(err)
	}
	members, err := st.ListSATenantMembers(ctx, t1)
	if err != nil {
		t.Fatal(err)
	}
	foundAdmin := false
	for _, member := range members {
		if member.UID == u2 && member.Role == "admin" {
			foundAdmin = true
		}
	}
	if !foundAdmin {
		t.Fatalf("invite downgraded admin: %+v", members)
	}
	s1, err := st.CreateSAServer(ctx, t1, "srv_one", "one")
	if err != nil {
		t.Fatal(err)
	}
	s2, err := st.CreateSAServer(ctx, t2, "srv_two", "two")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := st.SAServerByID(ctx, t1, s1.ID); err != nil || got.Name != "one" {
		t.Fatalf("own server = %+v %v", got, err)
	}
	if _, err := st.SAServerByID(ctx, t1, s2.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant server err = %v, want not found", err)
	}
	if err := st.UpdateSAServer(ctx, t1, s2.ID, "stolen", "active"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant update err = %v", err)
	}
	list, err := st.ListSAServers(ctx, t1, "")
	if err != nil || len(list) != 1 || list[0].ID != s1.ID {
		t.Fatalf("scoped list = %+v %v", list, err)
	}
	if err := st.RemoveSATenantMember(ctx, t1, 999999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant remove member err = %v", err)
	}
	if err := st.RemoveSATenantMember(ctx, t1, u1); !errors.Is(err, ErrLastOwner) {
		t.Fatalf("last owner remove err = %v", err)
	}
	platform, total, err := st.PlatformSATenants(ctx, "", 1, 20)
	if err != nil || total != 2 || len(platform) != 2 {
		t.Fatalf("platform tenants = %+v total=%d err=%v", platform, total, err)
	}
}

func TestServerAccessInviteTTLAtomicSingleUseAndReplay(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	owner := saSeedUser(t, st)
	invitee := saSeedUserAs(t, st, "invitee@example.com", "invitee")
	tenant, err := st.CreateSATenant(ctx, "invites", 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AddSATenantMember(ctx, tenant, owner, "owner"); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	hash := saTestHash("invite-token")
	if _, err := st.CreateSATenantInvite(ctx, tenant, "invitee@example.com", "admin", hash, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AcceptSATenantInvite(ctx, hash, invitee, "INVITEE@example.com", now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AcceptSATenantInvite(ctx, hash, invitee, "invitee@example.com", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("replay err = %v, want not found", err)
	}
	if _, err := st.CreateSATenantInvite(ctx, tenant, "other@example.com", "viewer", saTestHash("expired"), now.Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AcceptSATenantInvite(ctx, saTestHash("expired"), owner, "other@example.com", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired err = %v, want not found", err)
	}
	if _, err := st.AcceptSATenantInvite(ctx, saTestHash("expired"), owner, "wrong@example.com", now.Add(-2*time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong email err = %v, want not found", err)
	}

	raceHash := saTestHash("race")
	if _, err := st.CreateSATenantInvite(ctx, tenant, "invitee@example.com", "viewer", raceHash, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// The same invited account races itself: exactly one conditional UPDATE may
	// win, and the losing replay must not alter membership.
	start := make(chan struct{})
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = st.AcceptSATenantInvite(ctx, raceHash, invitee, "invitee@example.com", now)
		}(i)
	}
	close(start)
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrNotFound) {
			t.Fatalf("race err = %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("atomic acceptance wins = %d, want 1 (errs %v)", wins, errs)
	}
}

func TestServerAccessServerQuotaAndSuspendedInviteFailClosed(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := saSeedUser(t, st)
	tenant, _ := st.CreateSATenant(ctx, "limited", 1)
	server, err := st.CreateSAServer(ctx, tenant, "srv_limit", "one")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSAServer(ctx, tenant, "srv_over", "two"); !errors.Is(err, ErrQuota) {
		t.Fatalf("server over quota err = %v", err)
	}
	if err := st.UpdateSATenantStatus(ctx, tenant, "suspended"); err != nil {
		t.Fatal(err)
	}
	hash := saTestHash("suspended-invite")
	if _, err := st.CreateSATenantInvite(ctx, tenant, "invitee@example.com", "viewer", hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AcceptSATenantInvite(ctx, hash, uid, "invitee@example.com", time.Now()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("suspended tenant invite err = %v", err)
	}
	if _, err := st.SAServerByID(ctx, tenant, server.ID); err != nil {
		t.Fatalf("retained server unreadable in store: %v", err)
	}
}

func TestServerAccessCredentialRotationRevokeAndScope(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	tenant, _ := st.CreateSATenant(ctx, "cred", 2)
	otherTenant, _ := st.CreateSATenant(ctx, "other", 2)
	server, err := st.CreateSAServer(ctx, tenant, "srv_cred", "cred")
	if err != nil {
		t.Fatal(err)
	}
	otherServer, err := st.CreateSAServer(ctx, otherTenant, "srv_other_cred", "other")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	oldHash := saTestHash("old-secret")
	oldID, err := st.CreateSAServerCredential(ctx, tenant, server.ID, SACredential{Mode: "url_secret", SecretHash: oldHash, Scopes: `["verify:join"]`, Status: "active", ExpiresAt: sql.NullTime{Time: now.Add(48 * time.Hour), Valid: true}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := st.SACredentialBySecretHash(ctx, oldHash, now)
	if err != nil || got.TenantID != tenant || got.ServerID != server.ID {
		t.Fatalf("hash lookup = %+v %v", got, err)
	}
	if _, err := st.SACredentialBySecretHash(ctx, oldHash, now.Add(49*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired credential err = %v", err)
	}
	newHash := saTestHash("new-secret")
	newID, err := st.RotateSAServerCredential(ctx, tenant, server.ID, oldID, SACredential{Mode: "url_secret", SecretHash: newHash, Scopes: `["verify:join"]`, Status: "active", ExpiresAt: sql.NullTime{Time: now.Add(48 * time.Hour), Valid: true}}, 24*time.Hour, now)
	if err != nil || newID <= 0 {
		t.Fatalf("rotate = %d %v", newID, err)
	}
	if _, err := st.SACredentialBySecretHash(ctx, oldHash, now.Add(23*time.Hour)); err != nil {
		t.Fatalf("overlap old secret: %v", err)
	}
	if err := st.TouchSACredential(ctx, tenant, server.ID, oldID, now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if err := st.SetSAServerCredentialStatus(ctx, tenant, server.ID, oldID, "disabled"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SACredentialBySecretHash(ctx, oldHash, now.Add(23*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("disabled old secret err = %v", err)
	}
	if err := st.SetSAServerCredentialStatus(ctx, tenant, server.ID, oldID, "active"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SACredentialBySecretHash(ctx, oldHash, now.Add(25*time.Hour)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old secret after overlap err = %v", err)
	}
	if _, err := st.SACredentialBySecretHash(ctx, newHash, now); err != nil {
		t.Fatalf("new secret: %v", err)
	}
	if err := st.RevokeSAServerCredential(ctx, tenant, server.ID, newID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SACredentialBySecretHash(ctx, newHash, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked secret err = %v", err)
	}
	if err := st.RevokeSAServerCredential(ctx, tenant, otherServer.ID, newID, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant revoke err = %v", err)
	}
	if err := st.RevokeSAServerCredential(ctx, otherTenant, otherServer.ID, newID, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-server revoke err = %v", err)
	}
	if err := st.TouchSACredential(ctx, otherTenant, otherServer.ID, newID, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant touch err = %v", err)
	}
	if err := st.SetSAServerCredentialStatus(ctx, tenant, otherServer.ID, newID, "revoked"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-server status err = %v", err)
	}
	list, err := st.ListSAServerCredentials(ctx, tenant, server.ID)
	if err != nil || len(list) != 2 {
		t.Fatalf("credential list = %+v %v", list, err)
	}
	for _, c := range list {
		if c.SecretHash != "" {
			t.Fatal("credential metadata exposed secret hash")
		}
	}
}

func TestServerAccessPolicyWhitelistBanConstraintsAndScope(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := saSeedUser(t, st)
	tenant, _ := st.CreateSATenant(ctx, "policy", 2)
	other, _ := st.CreateSATenant(ctx, "other", 2)
	server, _ := st.CreateSAServer(ctx, tenant, "srv_policy", "policy")
	otherServer, _ := st.CreateSAServer(ctx, other, "srv_other_policy", "other")
	policy, err := st.SAServerPolicy(ctx, tenant, server.ID)
	if err != nil || !policy.PremiumFallbackEnabled || policy.WhitelistEnabled {
		t.Fatalf("default policy = %+v %v", policy, err)
	}
	policy.WhitelistEnabled = true
	policy.VerifyRateLimit = 42
	if err := st.UpdateSAServerPolicy(ctx, tenant, server.ID, policy); err != nil {
		t.Fatal(err)
	}
	if got, err := st.SAServerPolicy(ctx, tenant, server.ID); err != nil || !got.WhitelistEnabled || got.VerifyRateLimit != 42 {
		t.Fatalf("updated policy = %+v %v", got, err)
	}
	if err := st.UpdateSAServerPolicy(ctx, tenant, otherServer.ID, policy); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant policy err = %v", err)
	}
	policy.WhitelistLimit = 1
	if err := st.UpdateSAServerPolicy(ctx, tenant, server.ID, policy); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSAServerWhitelist(ctx, tenant, server.ID, uid, "note", uid); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSAServerWhitelist(ctx, tenant, server.ID, uid, "duplicate", uid); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate whitelist err = %v", err)
	}
	if err := st.AddSAServerWhitelist(ctx, tenant, otherServer.ID, uid, "cross", uid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant whitelist add err = %v", err)
	}
	if err := st.AddSAServerWhitelist(ctx, other, otherServer.ID, uid, "own", uid); err != nil {
		t.Fatal(err)
	}
	rows, err := st.ListSAServerWhitelist(ctx, tenant, server.ID)
	if err != nil || len(rows) != 1 || rows[0].UID != uid {
		t.Fatalf("whitelist = %+v %v", rows, err)
	}
	if err := st.RemoveSAServerWhitelist(ctx, tenant, server.ID, uid); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSAServerWhitelist(ctx, tenant, server.ID, uid, "again", uid); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSAServerWhitelist(ctx, tenant, server.ID, 999999, "over", uid); !errors.Is(err, ErrQuota) {
		t.Fatalf("whitelist over quota err = %v", err)
	}
	if err := st.RemoveSAServerWhitelist(ctx, tenant, otherServer.ID, uid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant whitelist remove err = %v", err)
	}
	expires := sql.NullTime{Time: time.Now().UTC().Add(time.Hour), Valid: true}
	if err := st.AddSAServerBan(ctx, tenant, server.ID, uid, "reason", expires, uid); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSAServerBan(ctx, tenant, server.ID, uid, "duplicate", expires, uid); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate ban err = %v", err)
	}
	bans, err := st.ListSAServerBans(ctx, tenant, server.ID)
	if err != nil || len(bans) != 1 || !bans[0].ExpiresAt.Valid {
		t.Fatalf("bans = %+v %v", bans, err)
	}
	if err := st.RemoveSAServerBan(ctx, tenant, server.ID, uid); err != nil {
		t.Fatal(err)
	}
	if err := st.RemoveSAServerBan(ctx, other, server.ID, uid); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant ban remove err = %v", err)
	}
}

func TestServerAccessScopedDeletesRetainGlobalUsers(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := saSeedUser(t, st)
	tenant, _ := st.CreateSATenant(ctx, "delete", 2)
	if err := st.AddSATenantMember(ctx, tenant, uid, "owner"); err != nil {
		t.Fatal(err)
	}
	server, err := st.CreateSAServer(ctx, tenant, "srv_delete", "delete")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteSAServer(ctx, tenant, server.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SAServerByID(ctx, tenant, server.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted server err = %v", err)
	}
	if err := st.DeleteSAServer(ctx, tenant, server.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("server delete replay err = %v", err)
	}
	if _, err := st.CreateSAServer(ctx, tenant, "srv_delete_two", "two"); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteSATenant(ctx, tenant); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SATenantByID(ctx, tenant); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted tenant err = %v", err)
	}
	if _, err := st.UserByID(ctx, uid); err != nil {
		t.Fatalf("tenant delete removed global user: %v", err)
	}
}

func saTestHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
