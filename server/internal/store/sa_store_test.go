package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
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
	u3 := saSeedUser(t, st)
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
	if err := st.AddSATenantMember(ctx, t1, u3, "admin"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSATenantInvite(ctx, t1, "second@example.com", "viewer", saTestHash("no-downgrade"), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AcceptSATenantInvite(ctx, saTestHash("no-downgrade"), u3, "second@example.com", time.Now()); err != nil {
		t.Fatal(err)
	}
	members, err := st.ListSATenantMembers(ctx, t1)
	if err != nil {
		t.Fatal(err)
	}
	foundAdmin := false
	for _, member := range members {
		if member.UID == u3 && member.Role == "admin" {
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

func TestServerAccessPlatformSearchTreatsWildcardsLiterally(t *testing.T) {
	saExercisePlatformSearchTreatsWildcardsLiterally(t, openSQLiteStore(t))
}

func saExercisePlatformSearchTreatsWildcardsLiterally(t *testing.T, st *Mysql) {
	t.Helper()
	ctx := context.Background()
	cases := []struct {
		name, match, decoy, query string
	}{
		{name: "ordinary", match: "ordinary-needle", decoy: "ordinary-other", query: "  needle  "},
		{name: "unicode", match: "磨砂团队-服务器", decoy: "ordinary-server", query: "磨砂团队"},
		{name: "single quote", match: "owner's server", decoy: "owners server", query: "owner's"},
		{name: "double quote", match: `say "hello"`, decoy: "say hello", query: `"hello"`},
		{name: "percent", match: "literal-percent%tenant", decoy: "literal-percentXtenant", query: "percent%"},
		{name: "underscore", match: "literal-under_score-tenant", decoy: "literal-underXscore-tenant", query: "under_score"},
		{name: "bang", match: "literal-bang!tenant", decoy: "literal-bangtenant", query: "bang!"},
		{name: "escaped bang and underscore", match: "escape!%_tenant", decoy: "escapeXXtenant", query: "!%_"},
	}
	type matchIDs struct {
		tenant int64
		server int64
	}
	want := make([]matchIDs, len(cases))
	for i, tc := range cases {
		tenant, err := st.CreateSATenant(ctx, tc.match, 1)
		if err != nil {
			t.Fatalf("create matching tenant %q: %v", tc.match, err)
		}
		server, err := st.CreateSAServer(ctx, tenant, fmt.Sprintf("srv_search_%d", i), tc.match)
		if err != nil {
			t.Fatalf("create matching server %q: %v", tc.match, err)
		}
		want[i] = matchIDs{tenant: tenant, server: server.ID}

		decoyTenant, err := st.CreateSATenant(ctx, tc.decoy, 1)
		if err != nil {
			t.Fatalf("create decoy tenant %q: %v", tc.decoy, err)
		}
		if _, err := st.CreateSAServer(ctx, decoyTenant, fmt.Sprintf("srv_search_decoy_%d", i), tc.decoy); err != nil {
			t.Fatalf("create decoy server %q: %v", tc.decoy, err)
		}
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tenants, tenantTotal, err := st.PlatformSATenants(ctx, tc.query, 1, 20)
			if err != nil {
				t.Fatalf("tenant search %q: %v", tc.query, err)
			}
			if tenantTotal != 1 || len(tenants) != 1 || tenants[0].ID != want[i].tenant {
				t.Fatalf("tenant search %q = %+v total=%d, want only tenant %d", tc.query, tenants, tenantTotal, want[i].tenant)
			}
			servers, serverTotal, err := st.PlatformSAServers(ctx, tc.query, 1, 20)
			if err != nil {
				t.Fatalf("server search %q: %v", tc.query, err)
			}
			if serverTotal != 1 || len(servers) != 1 || servers[0].ID != want[i].server {
				t.Fatalf("server search %q = %+v total=%d, want only server %d", tc.query, servers, serverTotal, want[i].server)
			}
		})
	}
}

func TestServerAccessRejectsSecondActiveTenantMembership(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := saSeedUserAs(t, st, "single-tenant@example.com", "single-tenant")
	first, _ := st.CreateSATenant(ctx, "first", 2)
	second, _ := st.CreateSATenant(ctx, "second", 2)
	if err := st.AddSATenantMember(ctx, first, uid, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSATenantMember(ctx, second, uid, "viewer"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("second active membership = %v, want forbidden", err)
	}
	hash := saTestHash("second-active-invite")
	if _, err := st.CreateSATenantInvite(ctx, second, "single-tenant@example.com", "admin", hash, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AcceptSATenantInvite(ctx, hash, uid, "single-tenant@example.com", time.Now()); !errors.Is(err, ErrForbidden) {
		t.Fatalf("invite into second active tenant = %v, want forbidden", err)
	}
	member, ok, err := st.SATenantMemberByUID(ctx, uid)
	if err != nil || !ok || member.TenantID != first {
		t.Fatalf("portal membership = %+v ok=%v err=%v", member, ok, err)
	}
}

func TestServerAccessCreateTenantWithOwner(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := saSeedUserAs(t, st, "atomic-owner@example.com", "atomic-owner")
	tenant, owner, err := st.CreateSATenantWithOwner(ctx, "atomic-tenant", 7, uid)
	if err != nil {
		t.Fatal(err)
	}
	if tenant.ID <= 0 || tenant.Name != "atomic-tenant" || tenant.Status != "active" || tenant.ServerLimit != 7 {
		t.Fatalf("tenant = %+v", tenant)
	}
	if owner.TenantID != tenant.ID || owner.UID != uid || owner.Role != "owner" || owner.Nickname != "atomic-owner" {
		t.Fatalf("owner = %+v", owner)
	}
	stored, err := st.SATenantByID(ctx, tenant.ID)
	if err != nil || stored != tenant {
		t.Fatalf("stored tenant = %+v err=%v", stored, err)
	}
	members, err := st.ListSATenantMembers(ctx, tenant.ID)
	if err != nil || len(members) != 1 || members[0] != owner {
		t.Fatalf("members = %+v err=%v", members, err)
	}
}

func TestServerAccessCreateTenantWithOwnerAuditedRollsBackAuditFailure(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := saSeedUser(t, st)
	actor := saSeedUser(t, st)
	if _, err := st.db.ExecContext(ctx, `CREATE TRIGGER fail_tenant_create_audit
		BEFORE INSERT ON audit_log WHEN NEW.action='serveraccess_tenant_create'
		BEGIN SELECT RAISE(ABORT,'forced audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	before, beforeTotal, err := st.PlatformSATenants(ctx, "", 1, 20)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateSATenantWithOwnerAudited(ctx, "unaudited-tenant", 7, uid, actor); err == nil {
		t.Fatal("tenant creation unexpectedly succeeded without audit")
	}
	after, afterTotal, err := st.PlatformSATenants(ctx, "", 1, 20)
	if err != nil || afterTotal != beforeTotal || len(after) != len(before) {
		t.Fatalf("tenant survived audit failure: after=%+v total=%d before=%+v total=%d err=%v", after, afterTotal, before, beforeTotal, err)
	}
	var memberships int
	if err := st.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sa_tenant_members WHERE uid=?`, uid).Scan(&memberships); err != nil {
		t.Fatal(err)
	}
	if memberships != 0 {
		t.Fatalf("owner membership survived audit failure: %d", memberships)
	}
	audit, total, err := st.PlatformServerAccessAudit(ctx, "serveraccess_tenant_create", 1, 20)
	if err != nil || total != 0 || len(audit) != 0 {
		t.Fatalf("failed tenant create audit = %+v total=%d err=%v", audit, total, err)
	}
}

func TestServerAccessCreateTenantWithOwnerAuditedCommitsAudit(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := saSeedUserAs(t, st, "audited-owner@example.com", "audited-owner")
	actor := saSeedUser(t, st)
	tenant, owner, err := st.CreateSATenantWithOwnerAudited(ctx, "audited-tenant", 7, uid, actor)
	if err != nil {
		t.Fatal(err)
	}
	if tenant.Status != "active" || tenant.ServerLimit != 7 || owner.TenantID != tenant.ID || owner.UID != uid || owner.Role != "owner" {
		t.Fatalf("audited creation = %+v owner=%+v", tenant, owner)
	}
	audit, total, err := st.PlatformServerAccessAudit(ctx, "serveraccess_tenant_create", 1, 20)
	if err != nil || total != 1 || len(audit) != 1 || audit[0]["actorUid"] != actor {
		t.Fatalf("tenant create audit = %+v total=%d err=%v", audit, total, err)
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(audit[0]["detail"].(string)), &detail); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"tenantId": float64(tenant.ID), "ownerUid": float64(uid), "serverLimit": float64(7)}
	if !reflect.DeepEqual(detail, want) {
		t.Fatalf("tenant create audit detail = %#v, want %#v", detail, want)
	}
}

func TestServerAccessCreateTenantWithOwnerRollsBack(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(*testing.T, *Mysql) int64
		err   error
	}{
		{name: "invalid owner", setup: func(*testing.T, *Mysql) int64 { return 999999 }, err: ErrNotFound},
		{name: "second active owner", setup: func(t *testing.T, st *Mysql) int64 {
			uid := saSeedUser(t, st)
			tenant, _ := st.CreateSATenant(context.Background(), "existing", 2)
			if err := st.AddSATenantMember(context.Background(), tenant, uid, "owner"); err != nil {
				t.Fatal(err)
			}
			return uid
		}, err: ErrForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := openSQLiteStore(t)
			ctx := context.Background()
			uid := tc.setup(t, st)
			before, _, err := st.PlatformSATenants(ctx, "", 1, 20)
			if err != nil {
				t.Fatal(err)
			}
			_, _, err = st.CreateSATenantWithOwner(ctx, tc.name, 2, uid)
			if !errors.Is(err, tc.err) {
				t.Fatalf("create err = %v, want %v", err, tc.err)
			}
			after, _, err := st.PlatformSATenants(ctx, "", 1, 20)
			if err != nil || len(after) != len(before) {
				t.Fatalf("tenants after rollback = %+v before=%+v err=%v", after, before, err)
			}
		})
	}
}

func TestServerAccessCreateTenantWithOwnerMembershipFailureRollsBack(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := saSeedUser(t, st)
	if _, err := st.db.ExecContext(ctx, `CREATE TRIGGER fail_atomic_owner
		BEFORE INSERT ON sa_tenant_members WHEN NEW.role='owner'
		BEGIN SELECT RAISE(ABORT, 'forced owner failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.CreateSATenantWithOwner(ctx, "forced-failure", 2, uid); err == nil {
		t.Fatal("membership failure unexpectedly succeeded")
	}
	tenants, total, err := st.PlatformSATenants(ctx, "", 1, 20)
	if err != nil || total != 0 || len(tenants) != 0 {
		t.Fatalf("tenants after membership failure = %+v total=%d err=%v", tenants, total, err)
	}
}

func TestServerAccessConcurrentCreateTenantWithOwnerAllowsOne(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := saSeedUser(t, st)
	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, _, errs[i] = st.CreateSATenantWithOwner(ctx, fmt.Sprintf("create-race-%d", i), 2, uid)
		}(i)
	}
	close(start)
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrForbidden) {
			t.Fatalf("create race err = %v, want forbidden", err)
		}
	}
	if wins != 1 {
		t.Fatalf("create race wins = %d, want 1 (errs %v)", wins, errs)
	}
	tenants, total, err := st.PlatformSATenants(ctx, "", 1, 20)
	if err != nil || total != 1 || len(tenants) != 1 {
		t.Fatalf("tenants after race = %+v total=%d err=%v", tenants, total, err)
	}
}

func TestServerAccessConcurrentMembershipCreationAllowsOneActiveTenant(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := saSeedUserAs(t, st, "membership-race@example.com", "membership-race")
	first, _ := st.CreateSATenant(ctx, "race-first", 2)
	second, _ := st.CreateSATenant(ctx, "race-second", 2)
	start := make(chan struct{})
	errs := make([]error, 2)
	var wg sync.WaitGroup
	for i, tenant := range []int64{first, second} {
		wg.Add(1)
		go func(i int, tenant int64) {
			defer wg.Done()
			<-start
			errs[i] = st.AddSATenantMember(ctx, tenant, uid, "viewer")
		}(i, tenant)
	}
	close(start)
	wg.Wait()
	wins := 0
	for _, err := range errs {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrForbidden) {
			t.Fatalf("membership race err = %v, want forbidden", err)
		}
	}
	if wins != 1 {
		t.Fatalf("active membership race wins = %d, want 1 (errs %v)", wins, errs)
	}
}

func TestServerAccessTenantReactivationCannotCreateSecondActiveMembership(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := saSeedUserAs(t, st, "reactivation@example.com", "reactivation")
	first, _ := st.CreateSATenant(ctx, "dormant", 2)
	second, _ := st.CreateSATenant(ctx, "current", 2)
	if err := st.AddSATenantMember(ctx, first, uid, "owner"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSATenantStatus(ctx, first, "suspended"); err != nil {
		t.Fatal(err)
	}
	if err := st.AddSATenantMember(ctx, second, uid, "viewer"); err != nil {
		t.Fatalf("membership while first tenant suspended: %v", err)
	}
	if err := st.UpdateSATenantStatus(ctx, first, "active"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("reactivation with second active membership = %v, want forbidden", err)
	}
	got, err := st.SATenantByID(ctx, first)
	if err != nil || got.Status != "suspended" {
		t.Fatalf("conflicting reactivation changed tenant: %+v %v", got, err)
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

func TestServerAccessQuotaLocksWaitForSQLiteWriter(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	tenant, _ := st.CreateSATenant(ctx, "lock-wait", 2)
	server, err := st.CreateSAServer(ctx, tenant, "srv_lock_wait", "lock")
	if err != nil {
		t.Fatal(err)
	}
	limit := 7
	if err := st.UpdateSAServerPlatformPolicy(ctx, tenant, server.ID, nil, nil, nil, &limit, nil); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		lock string
		call func(context.Context, *Mysql) error
	}{
		{
			name: "tenant row",
			lock: `UPDATE sa_tenants SET name=name WHERE id=?`,
			call: func(ctx context.Context, st *Mysql) error {
				tx, err := st.db.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				status, serverLimit, err := saLockTenantRow(ctx, tx, tenant)
				if err == nil && (status != "active" || serverLimit != 2) {
					return fmt.Errorf("locked tenant = %q limit %d", status, serverLimit)
				}
				return err
			},
		},
		{
			name: "policy row",
			lock: `UPDATE sa_server_policies SET whitelist_enabled=whitelist_enabled WHERE server_id=?`,
			call: func(ctx context.Context, st *Mysql) error {
				tx, err := st.db.BeginTx(ctx, nil)
				if err != nil {
					return err
				}
				defer tx.Rollback()
				got, err := saLockPolicyRow(ctx, tx, tenant, server.ID)
				if err == nil && got != limit {
					return fmt.Errorf("locked policy limit = %d, want %d", got, limit)
				}
				return err
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			holder, err := st.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer holder.Rollback()
			args := []any{tenant}
			if tc.name == "policy row" {
				args = []any{server.ID}
			}
			if _, err := holder.ExecContext(ctx, tc.lock, args...); err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			done := make(chan error, 1)
			go func() {
				close(started)
				done <- tc.call(ctx, st)
			}()
			<-started
			select {
			case err := <-done:
				t.Fatalf("lock did not wait for active writer: %v", err)
			case <-time.After(50 * time.Millisecond):
			}
			if err := holder.Commit(); err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil {
				t.Fatalf("lock after writer commit: %v", err)
			}
		})
	}
}

func TestServerAccessConcurrentServerCreateHonorsTenantQuota(t *testing.T) {
	saExerciseConcurrentServerQuota(t, openSQLiteStore(t))
}

func saExerciseConcurrentServerQuota(t *testing.T, st *Mysql) {
	t.Helper()
	ctx := context.Background()
	tenant, _ := st.CreateSATenant(ctx, "concurrent-servers", 3)
	const callers = 12
	start := make(chan struct{})
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = st.CreateSAServer(ctx, tenant, fmt.Sprintf("srv_concurrent_%d", i), "concurrent")
		}(i)
	}
	close(start)
	wg.Wait()
	wins := 0
	for i, err := range errs {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrQuota) {
			t.Fatalf("caller %d: create err = %v, want quota rejection", i, err)
		}
	}
	if wins != 3 {
		t.Fatalf("concurrent create wins = %d, want 3 (errs %v)", wins, errs)
	}
	rows, err := st.ListSAServers(ctx, tenant, "")
	if err != nil || len(rows) != 3 {
		t.Fatalf("server rows = %d, want 3: %v", len(rows), err)
	}
}

func TestServerAccessConcurrentWhitelistAddHonorsPolicyQuota(t *testing.T) {
	saExerciseConcurrentWhitelistQuota(t, openSQLiteStore(t))
}

func saExerciseConcurrentWhitelistQuota(t *testing.T, st *Mysql) {
	t.Helper()
	ctx := context.Background()
	tenant, _ := st.CreateSATenant(ctx, "concurrent-whitelist", 2)
	server, err := st.CreateSAServer(ctx, tenant, "srv_concurrent_whitelist", "concurrent")
	if err != nil {
		t.Fatal(err)
	}
	limit := 3
	if err := st.UpdateSAServerPlatformPolicy(ctx, tenant, server.ID, nil, nil, nil, &limit, nil); err != nil {
		t.Fatal(err)
	}
	const callers = 12
	start := make(chan struct{})
	errs := make([]error, callers)
	var wg sync.WaitGroup
	for i := 0; i < callers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = st.AddSAServerWhitelist(ctx, tenant, server.ID, int64(1000+i), "concurrent", 1)
		}(i)
	}
	close(start)
	wg.Wait()
	wins := 0
	for i, err := range errs {
		if err == nil {
			wins++
		} else if !errors.Is(err, ErrQuota) {
			t.Fatalf("caller %d: whitelist err = %v, want quota rejection", i, err)
		}
	}
	if wins != 3 {
		t.Fatalf("concurrent whitelist wins = %d, want 3 (errs %v)", wins, errs)
	}
	rows, err := st.ListSAServerWhitelist(ctx, tenant, server.ID)
	if err != nil || len(rows) != 3 {
		t.Fatalf("whitelist rows = %d, want 3: %v", len(rows), err)
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
	if err := st.TouchSACredential(ctx, tenant, server.ID, newID, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked credential touch err = %v, want not found", err)
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

func TestServerAccessOwnerServerUpdateCannotReactivateSuspended(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	tenant, _ := st.CreateSATenant(ctx, "suspension-race", 2)
	server, err := st.CreateSAServer(ctx, tenant, "srv_suspended", "before")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSAServer(ctx, tenant, server.ID, "before", "suspended"); err != nil {
		t.Fatal(err)
	}
	active := "active"
	if err := st.UpdateSAServerOwner(ctx, tenant, server.ID, "after", &active); !errors.Is(err, ErrForbidden) {
		t.Fatalf("owner reactivation after platform suspension = %v, want forbidden", err)
	}
	got, err := st.SAServerByID(ctx, tenant, server.ID)
	if err != nil || got.Status != "suspended" || got.Name != "before" {
		t.Fatalf("suspended server changed: %+v %v", got, err)
	}
	if err := st.UpdateSAServerOwner(ctx, tenant, server.ID, "renamed", nil); err != nil {
		t.Fatalf("owner rename on suspended server: %v", err)
	}
	got, err = st.SAServerByID(ctx, tenant, server.ID)
	if err != nil || got.Status != "suspended" || got.Name != "renamed" {
		t.Fatalf("name-only update changed suspension: %+v %v", got, err)
	}
}

func TestServerAccessOwnerServerUpdateAuditedRollsBackPolicyFailure(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	actor := saSeedUser(t, st)
	tenant, _ := st.CreateSATenant(ctx, "owner-policy-failure", 2)
	server, err := st.CreateSAServer(ctx, tenant, "srv_owner_policy_failure", "before")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `CREATE TRIGGER fail_owner_policy
		BEFORE UPDATE ON sa_server_policies WHEN NEW.whitelist_enabled=1
		BEGIN SELECT RAISE(ABORT,'forced policy failure'); END`); err != nil {
		t.Fatal(err)
	}
	name, status, whitelist, fallback := "after", "disabled", true, false
	if _, err := st.UpdateSAServerOwnerAudited(ctx, tenant, server.ID, SAOwnerServerPatch{
		Name: &name, Status: &status, WhitelistEnabled: &whitelist, PremiumFallbackEnabled: &fallback,
	}, actor); err == nil {
		t.Fatal("owner update unexpectedly succeeded with failing policy")
	}
	got, err := st.SAServerByID(ctx, tenant, server.ID)
	if err != nil || got.Name != "before" || got.Status != "active" {
		t.Fatalf("server fields survived policy failure: %+v %v", got, err)
	}
	policy, err := st.SAServerPolicy(ctx, tenant, server.ID)
	if err != nil || policy.WhitelistEnabled || !policy.PremiumFallbackEnabled {
		t.Fatalf("policy changed after failure: %+v %v", policy, err)
	}
	_, auditTotal, err := st.PlatformServerAccessAudit(ctx, "serveraccess_server_update", 1, 20)
	if err != nil || auditTotal != 0 {
		t.Fatalf("failed owner update wrote audit: total=%d err=%v", auditTotal, err)
	}
}

func TestServerAccessOwnerServerUpdateAuditedRollsBackAuditFailure(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	actor := saSeedUser(t, st)
	tenant, _ := st.CreateSATenant(ctx, "owner-audit-failure", 2)
	server, err := st.CreateSAServer(ctx, tenant, "srv_owner_audit_failure", "before")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, `CREATE TRIGGER fail_owner_update_audit
		BEFORE INSERT ON audit_log WHEN NEW.action='serveraccess_server_update'
		BEGIN SELECT RAISE(ABORT,'forced audit failure'); END`); err != nil {
		t.Fatal(err)
	}
	name, status, whitelist, fallback := "after", "disabled", true, false
	if _, err := st.UpdateSAServerOwnerAudited(ctx, tenant, server.ID, SAOwnerServerPatch{
		Name: &name, Status: &status, WhitelistEnabled: &whitelist, PremiumFallbackEnabled: &fallback,
	}, actor); err == nil {
		t.Fatal("owner update unexpectedly succeeded without audit")
	}
	got, err := st.SAServerByID(ctx, tenant, server.ID)
	if err != nil || got.Name != "before" || got.Status != "active" {
		t.Fatalf("server fields survived audit failure: %+v %v", got, err)
	}
	policy, err := st.SAServerPolicy(ctx, tenant, server.ID)
	if err != nil || policy.WhitelistEnabled || !policy.PremiumFallbackEnabled {
		t.Fatalf("policy survived audit failure: %+v %v", policy, err)
	}
	_, auditTotal, err := st.PlatformServerAccessAudit(ctx, "serveraccess_server_update", 1, 20)
	if err != nil || auditTotal != 0 {
		t.Fatalf("failed owner audit unexpectedly exists: total=%d err=%v", auditTotal, err)
	}
}

func TestServerAccessOwnerServerUpdateAuditedSparseFieldsAndAudit(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	actor := saSeedUser(t, st)
	tenant, _ := st.CreateSATenant(ctx, "owner-audited", 2)
	server, err := st.CreateSAServer(ctx, tenant, "srv_owner_audited", "before")
	if err != nil {
		t.Fatal(err)
	}
	rate, burst, concurrent, whitelistLimit, retention := 11, 12, 13, 14, 15
	if err := st.UpdateSAServerPlatformPolicy(ctx, tenant, server.ID, &rate, &burst, &concurrent, &whitelistLimit, &retention); err != nil {
		t.Fatal(err)
	}
	name, status, whitelist := "after", "disabled", true
	got, err := st.UpdateSAServerOwnerAudited(ctx, tenant, server.ID, SAOwnerServerPatch{
		Name: &name, Status: &status, WhitelistEnabled: &whitelist,
	}, actor)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != server.ID || got.TenantID != tenant || got.PublicID != server.PublicID || got.Name != "after" || got.Status != "disabled" || got.SourceMode != server.SourceMode || !got.CreatedAt.Equal(server.CreatedAt) {
		t.Fatalf("returned server = %+v, original=%+v", got, server)
	}
	stored, err := st.SAServerByID(ctx, tenant, server.ID)
	if err != nil || stored != got {
		t.Fatalf("stored server = %+v returned=%+v err=%v", stored, got, err)
	}
	policy, err := st.SAServerPolicy(ctx, tenant, server.ID)
	if err != nil || !policy.WhitelistEnabled || !policy.PremiumFallbackEnabled || policy.VerifyRateLimit != rate ||
		policy.VerifyBurst != burst || policy.MaxConcurrent != concurrent || policy.WhitelistLimit != whitelistLimit || policy.EventRetentionDays != retention {
		t.Fatalf("sparse owner patch crossed policy fields: %+v %v", policy, err)
	}
	audit, total, err := st.PlatformServerAccessAudit(ctx, "serveraccess_server_update", 1, 20)
	if err != nil || total != 1 || len(audit) != 1 || audit[0]["actorUid"] != actor {
		t.Fatalf("owner update audit = %+v total=%d err=%v", audit, total, err)
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(audit[0]["detail"].(string)), &detail); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"tenantId": float64(tenant), "serverId": float64(server.ID), "status": "disabled"}
	if !reflect.DeepEqual(detail, want) {
		t.Fatalf("owner update audit detail = %#v, want %#v", detail, want)
	}
}

func TestServerAccessOwnerServerUpdateAuditedEnforcesLifecycleUnderLock(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	actor := saSeedUser(t, st)
	tenant, _ := st.CreateSATenant(ctx, "owner-lifecycle", 2)
	server, err := st.CreateSAServer(ctx, tenant, "srv_owner_lifecycle", "before")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSATenantStatus(ctx, tenant, "suspended"); err != nil {
		t.Fatal(err)
	}
	name, status, whitelist := "after", "active", true
	if _, err := st.UpdateSAServerOwnerAudited(ctx, tenant, server.ID, SAOwnerServerPatch{
		Name: &name, Status: &status, WhitelistEnabled: &whitelist,
	}, actor); !errors.Is(err, ErrNotFound) {
		t.Fatalf("suspended tenant owner update = %v, want not found", err)
	}
	if err := st.UpdateSATenantStatus(ctx, tenant, "active"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSAServer(ctx, tenant, server.ID, "before", "suspended"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateSAServerOwnerAudited(ctx, tenant, server.ID, SAOwnerServerPatch{
		Name: &name, Status: &status, WhitelistEnabled: &whitelist,
	}, actor); !errors.Is(err, ErrForbidden) {
		t.Fatalf("suspended server status update = %v, want forbidden", err)
	}
	got, err := st.SAServerByID(ctx, tenant, server.ID)
	if err != nil || got.Name != "before" || got.Status != "suspended" {
		t.Fatalf("suspended server changed: %+v %v", got, err)
	}
	policy, err := st.SAServerPolicy(ctx, tenant, server.ID)
	if err != nil || policy.WhitelistEnabled {
		t.Fatalf("rejected status update changed policy: %+v %v", policy, err)
	}
	if _, err := st.RequestSATenantDeletion(ctx, tenant, DefaultSATenantDeletionGrace, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.UpdateSAServerOwnerAudited(ctx, tenant, server.ID, SAOwnerServerPatch{Name: &name}, actor); !errors.Is(err, ErrDeletionInProgress) {
		t.Fatalf("deleting tenant owner update = %v, want deletion in progress", err)
	}
	_, auditTotal, err := st.PlatformServerAccessAudit(ctx, "serveraccess_server_update", 1, 20)
	if err != nil || auditTotal != 0 {
		t.Fatalf("rejected owner updates wrote audit: total=%d err=%v", auditTotal, err)
	}
}

func TestServerAccessOwnerServerUpdateAuditedCannotOverwriteConcurrentSuspension(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	actor := saSeedUser(t, st)
	tenant, _ := st.CreateSATenant(ctx, "owner-suspension-race", 2)
	server, err := st.CreateSAServer(ctx, tenant, "srv_owner_suspension_race", "before")
	if err != nil {
		t.Fatal(err)
	}
	holder, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	if _, err := holder.ExecContext(ctx, `UPDATE sa_servers SET name=name WHERE id=?`, server.ID); err != nil {
		t.Fatal(err)
	}
	name, status, whitelist := "owner-name", "active", true
	ownerDone := make(chan error, 1)
	go func() {
		_, err := st.UpdateSAServerOwnerAudited(ctx, tenant, server.ID, SAOwnerServerPatch{
			Name: &name, Status: &status, WhitelistEnabled: &whitelist,
		}, actor)
		ownerDone <- err
	}()
	select {
	case err := <-ownerDone:
		t.Fatalf("owner update did not wait for locked server: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	platformDone := make(chan error, 1)
	go func() {
		platformDone <- st.UpdateSAServerPlatformAudited(ctx, tenant, server.ID, "before", "suspended", nil, nil, nil, nil, nil, actor)
	}()
	if err := holder.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := <-ownerDone; err != nil && !errors.Is(err, ErrForbidden) {
		t.Fatalf("owner update race err = %v, want success or forbidden", err)
	}
	if err := <-platformDone; err != nil {
		t.Fatalf("platform suspension: %v", err)
	}
	got, err := st.SAServerByID(ctx, tenant, server.ID)
	if err != nil || got.Status != "suspended" {
		t.Fatalf("concurrent suspension was overwritten: %+v %v", got, err)
	}
}

func TestServerAccessPolicyUpdatesRespectRoleFieldBoundaries(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	tenant, _ := st.CreateSATenant(ctx, "field-boundary", 2)
	other, _ := st.CreateSATenant(ctx, "field-boundary-other", 2)
	server, _ := st.CreateSAServer(ctx, tenant, "srv_fields", "fields")
	otherServer, _ := st.CreateSAServer(ctx, other, "srv_fields_other", "other")
	rate, burst, concurrent, whitelistLimit, retention := 11, 12, 13, 14, 15
	if err := st.UpdateSAServerPlatformPolicy(ctx, tenant, server.ID, &rate, &burst, &concurrent, &whitelistLimit, &retention); err != nil {
		t.Fatal(err)
	}
	newRate := 21
	if err := st.UpdateSAServerPlatformPolicy(ctx, tenant, server.ID, &newRate, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	enabled, disabled := true, false
	if err := st.UpdateSAServerOwnerPolicy(ctx, tenant, server.ID, &enabled, &disabled); err != nil {
		t.Fatal(err)
	}
	got, err := st.SAServerPolicy(ctx, tenant, server.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.WhitelistEnabled != true || got.PremiumFallbackEnabled != false ||
		got.VerifyRateLimit != 21 || got.VerifyBurst != 12 || got.MaxConcurrent != 13 ||
		got.WhitelistLimit != 14 || got.EventRetentionDays != 15 {
		t.Fatalf("role-specific policy update crossed fields: %+v", got)
	}
	if err := st.UpdateSAServerOwnerPolicy(ctx, tenant, server.ID, &disabled, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSAServerPlatformPolicy(ctx, other, server.ID, &newRate, nil, nil, nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant platform policy update = %v, want not found", err)
	}
	if err := st.UpdateSAServerOwnerPolicy(ctx, tenant, otherServer.ID, &enabled, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant owner policy update = %v, want not found", err)
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
	if err := st.UpdateSAServerOwnerPolicy(ctx, tenant, server.ID, &policy.WhitelistEnabled, nil); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSAServerPlatformPolicy(ctx, tenant, server.ID, &policy.VerifyRateLimit, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, err := st.SAServerPolicy(ctx, tenant, server.ID); err != nil || !got.WhitelistEnabled || got.VerifyRateLimit != 42 {
		t.Fatalf("updated policy = %+v %v", got, err)
	}
	if err := st.UpdateSAServerPlatformPolicy(ctx, tenant, otherServer.ID, &policy.VerifyRateLimit, nil, nil, nil, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant policy err = %v", err)
	}
	policy.WhitelistLimit = 1
	if err := st.UpdateSAServerPlatformPolicy(ctx, tenant, server.ID, nil, nil, nil, &policy.WhitelistLimit, nil); err != nil {
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

func TestServerAccessDeletingTenantRejectsNewCredentials(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	tenant, _ := st.CreateSATenant(ctx, "deleting-credential", 2)
	server, err := st.CreateSAServer(ctx, tenant, "srv_deleting_credential", "deleting")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.RequestSATenantDeletion(ctx, tenant, DefaultSATenantDeletionGrace, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSAServerCredential(ctx, tenant, server.ID, SACredential{
		Mode: "header", SecretHash: saTestHash("late-secret"), Scopes: `["verify:join"]`, Status: "active",
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("create credential on deleting tenant = %v, want not found", err)
	}
	rows, err := st.ListSAServerCredentials(ctx, tenant, server.ID)
	if err != nil || len(rows) != 0 {
		t.Fatalf("late credential rows = %+v %v, want none", rows, err)
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

type saZeroResult struct{}

func (saZeroResult) LastInsertId() (int64, error) { return 0, nil }
func (saZeroResult) RowsAffected() (int64, error) { return 0, nil }

func TestSAUpdateAffectedDistinguishesNoOpFromMissingScope(t *testing.T) {
	if err := saUpdateAffected(saZeroResult{}, nil, func() (bool, error) { return true, nil }); err != nil {
		t.Fatalf("scoped no-op update err = %v", err)
	}
	if err := saUpdateAffected(saZeroResult{}, nil, func() (bool, error) { return false, nil }); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing scoped update err = %v", err)
	}
	lookupErr := errors.New("lookup")
	if err := saUpdateAffected(saZeroResult{}, nil, func() (bool, error) { return false, lookupErr }); !errors.Is(err, lookupErr) {
		t.Fatalf("lookup failure = %v, want original error", err)
	}
	if err := saUpdateAffected(saZeroResult{}, lookupErr, func() (bool, error) { t.Fatal("lookup called after write failure"); return false, nil }); !errors.Is(err, lookupErr) {
		t.Fatalf("write failure = %v, want original error", err)
	}
}

func TestServerAccessVerificationEventsAndAtomicQuota(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	uid := saSeedUser(t, st)
	tenant, _ := st.CreateSATenant(ctx, "verify", 2)
	other, _ := st.CreateSATenant(ctx, "other", 2)
	server, _ := st.CreateSAServer(ctx, tenant, "srv_verify", "verify")
	now := time.Now().UTC().Truncate(time.Minute)
	allowed, err := st.SAWhitelistMember(ctx, tenant, server.ID, uid)
	if err != nil || allowed {
		t.Fatalf("missing whitelist = %v, %v", allowed, err)
	}
	if err := st.AddSAServerWhitelist(ctx, tenant, server.ID, uid, "yes", uid); err != nil {
		t.Fatal(err)
	}
	if allowed, err := st.SAWhitelistMember(ctx, other, server.ID, uid); err != nil || allowed {
		t.Fatalf("cross-tenant whitelist = %v, %v", allowed, err)
	}
	if allowed, err = st.SAWhitelistMember(ctx, tenant, server.ID, uid); err != nil || !allowed {
		t.Fatalf("whitelist member = %v, %v", allowed, err)
	}
	if banned, err := st.SAActiveBan(ctx, tenant, server.ID, uid, now); err != nil || banned {
		t.Fatalf("missing ban = %v, %v", banned, err)
	}
	if err := st.AddSAServerBan(ctx, tenant, server.ID, uid, "ban", sql.NullTime{Time: now.Add(time.Minute), Valid: true}, uid); err != nil {
		t.Fatal(err)
	}
	if banned, err := st.SAActiveBan(ctx, tenant, server.ID, uid, now); err != nil || !banned {
		t.Fatalf("active ban = %v, %v", banned, err)
	}
	if banned, err := st.SAActiveBan(ctx, tenant, server.ID, uid, now.Add(2*time.Minute)); err != nil || banned {
		t.Fatalf("expired ban = %v, %v", banned, err)
	}
	event := SAJoinEvent{TenantID: tenant, ServerID: server.ID, JoinSessionID: sql.NullInt64{Int64: 42, Valid: true},
		UID: sql.NullInt64{Int64: uid, Valid: true}, UUID: "0123456789abcdef0123456789abcdef", PlayerName: "Alice",
		Outcome: "allow", ReasonCode: "local_session", SourceMode: "url_secret", DurationMS: 12, RequestID: "req-1"}
	if err := st.InsertSAJoinEvent(ctx, event); err != nil {
		t.Fatal(err)
	}
	if err := st.InsertSAJoinEvent(ctx, event); err != nil {
		t.Fatalf("event replay must be idempotent: %v", err)
	}
	events, err := st.ListSAJoinEvents(ctx, tenant, server.ID, now.Add(-time.Minute), now.Add(time.Minute), 20)
	if err != nil || len(events) != 1 || events[0].PlayerName != "Alice" || events[0].SourceMode != "url_secret" || events[0].TenantID != tenant {
		t.Fatalf("events = %+v, %v", events, err)
	}
	if events[0].TrustLevel() != "possessed_credential" {
		t.Fatalf("event trust level = %q", events[0].TrustLevel())
	}
	headerEvent := event
	headerEvent.SourceMode = "header"
	if headerEvent.TrustLevel() != "header_credential" {
		t.Fatalf("header event trust level = %q", headerEvent.TrustLevel())
	}
	if err := st.InsertSAJoinEvent(ctx, SAJoinEvent{TenantID: other, ServerID: server.ID, PlayerName: "Cross", Outcome: "deny", ReasonCode: "test", SourceMode: "url_secret"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("cross-tenant event insert err = %v, want not found", err)
	}
	if events, err = st.ListSAJoinEvents(ctx, other, server.ID, now.Add(-time.Minute), now.Add(time.Minute), 20); err != nil || len(events) != 0 {
		t.Fatalf("cross-tenant events = %+v, %v", events, err)
	}
	const workers = 12
	start := make(chan struct{})
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, _, errs[i] = st.AdmitSAQuota(ctx, server.ID, now, "verify", workers, now)
		}(i)
	}
	close(start)
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if got, err := st.ReadSAQuota(ctx, server.ID, now, "verify"); err != nil || got != workers {
		t.Fatalf("quota = %d, %v, want %d", got, err, workers)
	}
	if got, err := st.ReadSAQuota(ctx, server.ID, now.Add(time.Minute), "verify"); err != nil || got != 0 {
		t.Fatalf("new window quota = %d, %v", got, err)
	}
}

func TestServerAccessQuotaAdmissionReturnsItsOwnIncrement(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	tenant, _ := st.CreateSATenant(ctx, "quota-own-increment", 2)
	server, _ := st.CreateSAServer(ctx, tenant, "srv_quota_own_increment", "quota")
	window := time.Now().UTC().Truncate(time.Minute)
	firstUsed, firstAllowed, err := st.AdmitSAQuota(ctx, server.ID, window, "verify", 1, window.Add(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	secondUsed, secondAllowed, err := st.AdmitSAQuota(ctx, server.ID, window, "verify", 1, window.Add(20*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if firstUsed != 1 || !firstAllowed || secondUsed != 2 || secondAllowed {
		t.Fatalf("admissions = first(%d,%v) second(%d,%v)", firstUsed, firstAllowed, secondUsed, secondAllowed)
	}
}

func TestServerAccessQuotaPurgeDeletesOnlyExpiredWindows(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	tenant, _ := st.CreateSATenant(ctx, "quota-purge", 2)
	server, _ := st.CreateSAServer(ctx, tenant, "srv_quota_purge", "quota")
	now := time.Now().UTC().Truncate(time.Second)
	if _, _, err := st.AdmitSAQuotaWindow(ctx, server.ID, now.Add(-2*time.Minute), "verify", 10, time.Minute, now); err != nil {
		t.Fatal(err)
	}
	if _, _, err := st.AdmitSAQuotaWindow(ctx, server.ID, now, "verify_burst", 10, time.Second, now); err != nil {
		t.Fatal(err)
	}
	removed, err := st.PurgeSAQuotaUsage(ctx, now, 100)
	if err != nil || removed != 1 {
		t.Fatalf("purge = (%d, %v), want one expired row", removed, err)
	}
	var count int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM sa_quota_usage WHERE server_id=?`, server.ID).Scan(&count); err != nil || count != 1 {
		t.Fatalf("quota rows after purge = %d, %v, want current row only", count, err)
	}
}

func TestServerAccessQuotaAdmissionConcurrentLimit(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	tenant, _ := st.CreateSATenant(ctx, "quota-admission", 2)
	server, _ := st.CreateSAServer(ctx, tenant, "srv_quota_admission", "quota")
	window := time.Now().UTC().Truncate(time.Minute)
	const workers, limit = 12, 5
	start := make(chan struct{})
	allowedResults := make([]bool, workers)
	errs := make([]error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, allowedResults[i], errs[i] = st.AdmitSAQuota(ctx, server.ID, window, "verify", limit, window.Add(30*time.Second))
		}(i)
	}
	close(start)
	wg.Wait()
	allowed := 0
	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
		if allowedResults[i] {
			allowed++
		}
	}
	if allowed != limit {
		t.Fatalf("concurrent admissions = %d, want %d", allowed, limit)
	}
	if got, err := st.ReadSAQuota(ctx, server.ID, window, "verify"); err != nil || got != workers {
		t.Fatalf("quota = %d, %v, want %d", got, err, workers)
	}
}

func TestServerAccessQuotaAdmissionMinuteBoundaryUsesOneSnapshot(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	tenant, _ := st.CreateSATenant(ctx, "quota-boundary", 2)
	server, _ := st.CreateSAServer(ctx, tenant, "srv_quota_boundary", "quota")
	window := time.Now().UTC().Truncate(time.Minute)
	st.now = func() time.Time {
		t.Fatal("quota admission must use the supplied snapshot")
		return window
	}
	for _, tc := range []struct {
		name        string
		window, now time.Time
		wantUsed    int64
		wantAllowed bool
	}{
		{name: "last millisecond of minute", window: window, now: window.Add(time.Minute - time.Millisecond), wantUsed: 1, wantAllowed: true},
		{name: "limit exceeded", window: window, now: window.Add(time.Minute - time.Millisecond), wantUsed: 2, wantAllowed: false},
		{name: "next minute", window: window.Add(time.Minute), now: window.Add(time.Minute), wantUsed: 1, wantAllowed: true},
		{name: "expired old window resets", window: window, now: window.Add(2 * time.Minute), wantUsed: 1, wantAllowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			used, allowed, err := st.AdmitSAQuota(ctx, server.ID, tc.window, "verify", 1, tc.now)
			if err != nil || used != tc.wantUsed || allowed != tc.wantAllowed {
				t.Fatalf("admission = (%d, %v, %v), want (%d, %v)", used, allowed, err, tc.wantUsed, tc.wantAllowed)
			}
		})
	}
}

func saTestHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}
