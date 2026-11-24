package store

import (
	"context"
	"testing"
	"time"
)

func TestSAEventRetentionDeletesOnlyExpiredServerEventsInBatches(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	tenant, err := st.CreateSATenant(ctx, "retention", 10)
	if err != nil {
		t.Fatal(err)
	}
	server, err := st.CreateSAServer(ctx, tenant, "srv_retention", "retention")
	if err != nil {
		t.Fatal(err)
	}
	other, err := st.CreateSAServer(ctx, tenant, "srv_retention_other", "other")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	cutoff := now.Add(-7 * 24 * time.Hour)
	for i := 0; i < 3; i++ {
		created := cutoff.Add(-time.Duration(i+1) * time.Hour)
		if _, err := st.DB().Exec(`INSERT INTO sa_join_events
			(tenant_id,server_id,uuid,player_name,outcome,reason_code,source_mode,duration_ms,request_id,created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?)`, tenant, server.ID, "", "Old", "deny", "test", "header", 0, "old", created); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []struct {
		server int64
		name   string
		at     time.Time
	}{
		{server.ID, "Boundary", cutoff},
		{server.ID, "Fresh", cutoff.Add(time.Hour)},
		{other.ID, "OtherOld", cutoff.Add(-24 * time.Hour)},
	} {
		if _, err := st.DB().Exec(`INSERT INTO sa_join_events
			(tenant_id,server_id,uuid,player_name,outcome,reason_code,source_mode,duration_ms,request_id,created_at)
			VALUES (?,?,?,?,?,?,?,?,?,?)`, tenant, row.server, "", row.name, "deny", "test", "header", 0, row.name, row.at); err != nil {
			t.Fatal(err)
		}
	}
	var deleted int64
	for {
		n, err := st.PurgeSAJoinEvents(ctx, server.ID, cutoff, 2)
		if err != nil {
			t.Fatal(err)
		}
		deleted += n
		if n < 2 {
			break
		}
	}
	if deleted != 3 {
		t.Fatalf("deleted = %d, want 3", deleted)
	}
	events, err := st.ListSAJoinEvents(ctx, tenant, server.ID, cutoff.Add(-48*time.Hour), now, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].PlayerName != "Fresh" || events[1].PlayerName != "Boundary" {
		t.Fatalf("remaining server events = %+v", events)
	}
	otherEvents, err := st.ListSAJoinEvents(ctx, tenant, other.ID, cutoff.Add(-48*time.Hour), now, 20)
	if err != nil || len(otherEvents) != 1 || otherEvents[0].PlayerName != "OtherOld" {
		t.Fatalf("other server events = %+v, %v", otherEvents, err)
	}
}

func TestSAEventRetentionPoliciesIncludeDisabledServersButNotDeletedTenants(t *testing.T) {
	st := openSQLiteStore(t)
	ctx := context.Background()
	tenant, err := st.CreateSATenant(ctx, "retention", 10)
	if err != nil {
		t.Fatal(err)
	}
	server, err := st.CreateSAServer(ctx, tenant, "srv_retention", "retention")
	if err != nil {
		t.Fatal(err)
	}
	if err := st.UpdateSAServer(ctx, tenant, server.ID, server.Name, "suspended"); err != nil {
		t.Fatal(err)
	}
	days := 9
	if err := st.UpdateSAServerPlatformPolicy(ctx, tenant, server.ID, nil, nil, nil, nil, &days); err != nil {
		t.Fatal(err)
	}
	deletedTenant, err := st.CreateSATenant(ctx, "deleted", 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateSAServer(ctx, deletedTenant, "srv_deleted", "deleted"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().Exec(`UPDATE sa_tenants SET deleted_at=? WHERE id=?`, time.Now().UTC(), deletedTenant); err != nil {
		t.Fatal(err)
	}
	policies, err := st.SAEventRetentionPolicies(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(policies) != 1 || policies[0].ServerID != server.ID || policies[0].EventRetentionDays != 9 {
		t.Fatalf("retention policies = %+v", policies)
	}
}
