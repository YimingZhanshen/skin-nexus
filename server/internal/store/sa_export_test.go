package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestServerAccessExportRejectsRowLimitOverflowWithoutTruncation(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		collection string
		limitField string
		seed       func(*testing.T, *Mysql, int64, int64, int, int)
	}{
		{"servers", "MaxServers", func(t *testing.T, st *Mysql, tenant, _ int64, count, offset int) {
			for i := 0; i < count; i++ {
				if _, err := st.CreateSAServer(ctx, tenant, fmt.Sprintf("srv_export_limit_%d_%d", saSeedSeq.Add(1), i), "limit"); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"whitelist", "MaxWhitelist", func(t *testing.T, st *Mysql, tenant, server int64, count, offset int) {
			for i := 0; i < count; i++ {
				if err := st.AddSAServerWhitelist(ctx, tenant, server, int64(10000+offset+i), "note", 1); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"bans", "MaxBans", func(t *testing.T, st *Mysql, tenant, server int64, count, offset int) {
			for i := 0; i < count; i++ {
				if err := st.AddSAServerBan(ctx, tenant, server, int64(10000+offset+i), "reason", sql.NullTime{}, 1); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"events", "MaxEvents", func(t *testing.T, st *Mysql, tenant, server int64, count, offset int) {
			for i := 0; i < count; i++ {
				if err := st.InsertSAJoinEvent(ctx, SAJoinEvent{
					TenantID: tenant, ServerID: server, Outcome: "allow", ReasonCode: "local_session",
					SourceMode: "header", RequestID: fmt.Sprintf("export-limit-%d", offset+i),
				}); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"credentials", "MaxCredentials", func(t *testing.T, st *Mysql, tenant, server int64, count, offset int) {
			for i := 0; i < count; i++ {
				if _, err := st.CreateSAServerCredential(ctx, tenant, server, SACredential{
					Mode: "header", SecretHash: saTestHash(fmt.Sprintf("export-limit-%d", offset+i)), Scopes: `["verify:join"]`, Status: "active",
				}); err != nil {
					t.Fatal(err)
				}
			}
		}},
	} {
		t.Run(tc.collection, func(t *testing.T) {
			st := openSQLiteStore(t)
			tenant, _ := st.CreateSATenant(ctx, "export-limit", 10)
			server, err := st.CreateSAServer(ctx, tenant, "srv_export_limit_base", "limit")
			if err != nil {
				t.Fatal(err)
			}
			if tc.collection == "servers" {
				if _, err := st.DB().ExecContext(ctx, `DELETE FROM sa_servers WHERE id=?`, server.ID); err != nil {
					t.Fatal(err)
				}
			}
			tc.seed(t, st, tenant, server.ID, 2, 0)
			limits := DefaultSAExportLimits()
			switch tc.limitField {
			case "MaxServers":
				limits.MaxServers = 2
			case "MaxWhitelist":
				limits.MaxWhitelist = 2
			case "MaxBans":
				limits.MaxBans = 2
			case "MaxEvents":
				limits.MaxEvents = 2
			case "MaxCredentials":
				limits.MaxCredentials = 2
			}
			data, err := st.SAExportData(ctx, tenant, limits)
			if err != nil {
				t.Fatalf("exact limit: %v", err)
			}
			if data.Servers == nil {
				t.Fatal("exact-limit export returned nil servers")
			}
			tc.seed(t, st, tenant, server.ID, 1, 2)
			_, err = st.SAExportData(ctx, tenant, limits)
			var limitErr *SAExportLimitError
			if !errors.As(err, &limitErr) || !errors.Is(err, ErrSAExportTooLarge) {
				t.Fatalf("over limit = %v, want typed export-too-large error", err)
			}
			if limitErr.Collection != tc.collection || limitErr.Limit != int64(limitForField(limits, tc.limitField)) {
				t.Fatalf("limit error = %+v, want collection %s", limitErr, tc.collection)
			}
		})
	}
}

func TestServerAccessExportLimitsCannotExceedHardCaps(t *testing.T) {
	defaults := DefaultSAExportLimits()
	raised := SAExportLimits{
		MaxServers: defaults.MaxServers + 1, MaxWhitelist: defaults.MaxWhitelist + 1,
		MaxBans: defaults.MaxBans + 1, MaxEvents: defaults.MaxEvents + 1,
		MaxCredentials: defaults.MaxCredentials + 1, MaxBytes: defaults.MaxBytes + 1,
	}
	if got := ResolveSAExportLimits(raised); got != defaults {
		t.Fatalf("raised limits resolved to %+v, want hard caps %+v", got, defaults)
	}
	lowered := SAExportLimits{MaxServers: 2, MaxWhitelist: 3, MaxBans: 4, MaxEvents: 5, MaxCredentials: 6, MaxBytes: 7}
	if got := ResolveSAExportLimits(lowered); got != lowered {
		t.Fatalf("lowered limits resolved to %+v, want %+v", got, lowered)
	}
	if got := ResolveSAExportLimits(SAExportLimits{}); got != defaults {
		t.Fatalf("zero limits resolved to %+v, want defaults %+v", got, defaults)
	}
}

func TestServerAccessExportUsesBoundedQueriesIndependentOfServerCount(t *testing.T) {
	st, recorder := openSARecordingSQLiteStore(t)
	ctx := context.Background()
	tenant, _ := st.CreateSATenant(ctx, "export-batch", 10)
	for i := 0; i < 3; i++ {
		if _, err := st.CreateSAServer(ctx, tenant, fmt.Sprintf("srv_export_batch_%d", i), "batch"); err != nil {
			t.Fatal(err)
		}
	}
	recorder.reset()
	if _, err := st.SAExportData(ctx, tenant, DefaultSAExportLimits()); err != nil {
		t.Fatal(err)
	}
	statements := recorder.statements()
	if len(statements) != 6 {
		t.Fatalf("export statements = %d, want 6 batch queries: %v", len(statements), statements)
	}
	for _, table := range []string{"sa_servers", "sa_server_policies", "sa_server_whitelist", "sa_server_bans", "sa_join_events", "sa_server_credentials"} {
		found := 0
		for _, statement := range statements {
			if strings.Contains(statement, "FROM "+table) {
				found++
			}
		}
		if found != 1 {
			t.Fatalf("table %s queries = %d, want one: %v", table, found, statements)
		}
	}
}

func TestServerAccessExportNeverLoadsCredentialSecrets(t *testing.T) {
	st, recorder := openSARecordingSQLiteStore(t)
	ctx := context.Background()
	tenant, _ := st.CreateSATenant(ctx, "export-secret", 2)
	server, _ := st.CreateSAServer(ctx, tenant, "srv_export_secret", "secret")
	if _, err := st.CreateSAServerCredential(ctx, tenant, server.ID, SACredential{
		Mode: "header", SecretHash: saTestHash("export-secret-hash"), Scopes: `["verify:join","private:scope"]`, Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	recorder.reset()
	data, err := st.SAExportData(ctx, tenant, DefaultSAExportLimits())
	if err != nil {
		t.Fatal(err)
	}
	rows := data.Credentials[server.ID]
	if len(rows) != 1 {
		t.Fatalf("export credentials = %+v", rows)
	}
	for _, statement := range recorder.statements() {
		if strings.Contains(statement, "sa_server_credentials") && (strings.Contains(statement, "secret_hash") || strings.Contains(statement, "scopes")) {
			t.Fatalf("export query loaded credential secret material: %s", statement)
		}
	}
}

func limitForField(limits SAExportLimits, field string) int {
	switch field {
	case "MaxServers":
		return limits.MaxServers
	case "MaxWhitelist":
		return limits.MaxWhitelist
	case "MaxBans":
		return limits.MaxBans
	case "MaxEvents":
		return limits.MaxEvents
	default:
		return limits.MaxCredentials
	}
}
