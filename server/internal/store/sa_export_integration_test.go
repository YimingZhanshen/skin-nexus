//go:build integration

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"testing"
)

func TestIntegrationServerAccessExportBatchingAndLimits(t *testing.T) {
	st := openTestStore(t)
	ctx := context.Background()
	tenant, err := st.CreateSATenant(ctx, "export-integration", 10)
	if err != nil {
		t.Fatal(err)
	}
	first, err := st.CreateSAServer(ctx, tenant, "srv_export_integration_first", "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := st.CreateSAServer(ctx, tenant, "srv_export_integration_second", "second")
	if err != nil {
		t.Fatal(err)
	}
	for i, serverID := range []int64{first.ID, second.ID} {
		if err := st.AddSAServerWhitelist(ctx, tenant, serverID, int64(3000+i), "note", 1); err != nil {
			t.Fatal(err)
		}
		if err := st.AddSAServerBan(ctx, tenant, serverID, int64(3000+i), "reason", sql.NullTime{}, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := st.CreateSAServerCredential(ctx, tenant, serverID, SACredential{
			Mode: "header", SecretHash: saTestHash(fmt.Sprintf("export-integration-%d", i)), Scopes: `["verify:join"]`, Status: "active",
		}); err != nil {
			t.Fatal(err)
		}
		if err := st.InsertSAJoinEvent(ctx, SAJoinEvent{
			TenantID: tenant, ServerID: serverID, Outcome: "allow", ReasonCode: "local_session", SourceMode: "header",
		}); err != nil {
			t.Fatal(err)
		}
	}
	data, err := st.SAExportData(ctx, tenant, SAExportLimits{
		MaxServers: 2, MaxWhitelist: 2, MaxBans: 2, MaxEvents: 2, MaxCredentials: 2,
	})
	if err != nil {
		t.Fatalf("exact export limits: %v", err)
	}
	if len(data.Servers) != 2 || len(data.Policies) != 2 || len(data.Whitelist[first.ID]) != 1 || len(data.Whitelist[second.ID]) != 1 ||
		len(data.Bans[first.ID]) != 1 || len(data.Bans[second.ID]) != 1 || len(data.Events[first.ID]) != 1 || len(data.Events[second.ID]) != 1 ||
		len(data.Credentials[first.ID]) != 1 || len(data.Credentials[second.ID]) != 1 {
		t.Fatalf("batched export data = %+v", data)
	}
	_, err = st.SAExportData(ctx, tenant, SAExportLimits{MaxWhitelist: 1})
	var limitErr *SAExportLimitError
	if !errors.As(err, &limitErr) || !errors.Is(err, ErrSAExportTooLarge) || limitErr.Collection != "whitelist" || limitErr.Limit != 1 {
		t.Fatalf("overflow export = %v, want whitelist limit 1", err)
	}
}
