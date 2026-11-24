package serveraccess

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/store"
)

func saAssertKeys(t *testing.T, object map[string]any, keys ...string) {
	t.Helper()
	if len(object) != len(keys) {
		t.Fatalf("object keys = %#v, want exactly %v", object, keys)
	}
	for _, key := range keys {
		if _, ok := object[key]; !ok {
			t.Fatalf("object missing %q: %#v", key, object)
		}
	}
}

func saAuditDetail(t *testing.T, f *saFixture, action string) string {
	t.Helper()
	var detail string
	if err := f.st.DB().QueryRowContext(context.Background(),
		`SELECT detail FROM audit_log WHERE action=? ORDER BY id DESC LIMIT 1`, action).Scan(&detail); err != nil {
		t.Fatalf("read audit %s: %v", action, err)
	}
	return detail
}

func TestServerAccessAPITenantExportUsesStrictFieldAllowlist(t *testing.T) {
	f := newSAFixture(t)
	ctx := context.Background()
	playerID, playerUUID, err := f.st.CreatePlayer(ctx, f.outsider, "GlobalPlayer")
	if err != nil {
		t.Fatal(err)
	}
	textureHash := strings.Repeat("c", 64)
	textureID, err := f.st.AddTexture(ctx, f.outsider, "skin", textureHash, "default", store.TextureMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.DB().ExecContext(ctx, `UPDATE users SET ip=? WHERE id=?`, "203.0.113.10", f.outsider); err != nil {
		t.Fatal(err)
	}
	activeHash, revokedHash := strings.Repeat("d", 64), strings.Repeat("e", 64)
	activeID, err := f.st.CreateSAServerCredential(ctx, f.tenant, f.server, store.SACredential{
		Mode: "url_secret", SecretHash: activeHash, Scopes: `["verify:join","private:scope"]`, Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.TouchSACredential(ctx, f.tenant, f.server, activeID, f.now); err != nil {
		t.Fatal(err)
	}
	expires := f.now.Add(36 * time.Hour)
	revokedID, err := f.st.CreateSAServerCredential(ctx, f.tenant, f.server, store.SACredential{
		Mode: "header", SecretHash: revokedHash, Scopes: `["verify:join"]`, Status: "revoked",
		ExpiresAt: sql.NullTime{Time: expires, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.AddSAServerWhitelist(ctx, f.tenant, f.server, f.outsider, "allowed note", f.owner); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AddSAServerBan(ctx, f.tenant, f.server, f.outsider, "ban reason", sql.NullTime{Time: expires, Valid: true}, f.owner); err != nil {
		t.Fatal(err)
	}
	if err := f.st.InsertSAJoinEvent(ctx, store.SAJoinEvent{
		TenantID: f.tenant, ServerID: f.server, JoinSessionID: sql.NullInt64{Int64: 9876, Valid: true},
		UID:  sql.NullInt64{Int64: f.outsider, Valid: true},
		UUID: playerUUID, PlayerName: "PrivatePlayer", Outcome: "allow", ReasonCode: "local_session",
		SourceMode: "url_secret", DurationMS: 17, RequestID: "private-request-id",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CreateSATenantInvite(ctx, f.tenant, "invitee@example.com", "viewer", strings.Repeat("f", 64), f.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}

	path := "/api/serveraccess/v1/tenants/me/export?tenant_id=" + fmt.Sprint(f.otherTenant)
	rec := f.request(t, f.owner, http.MethodGet, path, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("owner export = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(got, "attachment;") || !strings.Contains(got, fmt.Sprintf("serveraccess-tenant-%d.json", f.tenant)) {
		t.Fatalf("Content-Disposition = %q, want tenant JSON attachment", got)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Fatalf("Content-Type = %q, want application/json", got)
	}
	var document map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode export: %v", err)
	}
	saAssertKeys(t, document, "schemaVersion", "exportedAt", "tenant", "servers")
	if document["schemaVersion"] != float64(1) || document["exportedAt"] == nil {
		t.Fatalf("export envelope = %#v", document)
	}
	tenant := document["tenant"].(map[string]any)
	saAssertKeys(t, tenant, "id", "name", "status", "serverLimit", "createdAt", "deletionRequestedAt", "deletionDueAt")
	if tenant["id"] != float64(f.tenant) || tenant["name"] != "one" || tenant["status"] != "active" || tenant["serverLimit"] != float64(10) {
		t.Fatalf("export tenant = %#v", tenant)
	}
	servers := document["servers"].([]any)
	if len(servers) != 1 {
		t.Fatalf("export servers = %#v, want only current tenant server", servers)
	}
	server := servers[0].(map[string]any)
	saAssertKeys(t, server, "id", "publicId", "name", "status", "sourceMode", "createdAt", "policy", "whitelist", "bans", "events", "credentials")
	if server["id"] != float64(f.server) || server["publicId"] != "srv_one" || server["name"] != "one" {
		t.Fatalf("export server = %#v", server)
	}
	policy := server["policy"].(map[string]any)
	saAssertKeys(t, policy, "whitelistEnabled", "premiumFallbackEnabled", "verifyRateLimit", "verifyBurst", "maxConcurrent", "whitelistLimit", "eventRetentionDays")
	whitelist := server["whitelist"].([]any)
	if len(whitelist) != 1 {
		t.Fatalf("export whitelist = %#v", whitelist)
	}
	whitelistRow := whitelist[0].(map[string]any)
	saAssertKeys(t, whitelistRow, "uid", "note", "createdAt")
	if whitelistRow["uid"] != float64(f.outsider) || whitelistRow["note"] != "allowed note" {
		t.Fatalf("export whitelist row = %#v", whitelistRow)
	}
	bans := server["bans"].([]any)
	if len(bans) != 1 {
		t.Fatalf("export bans = %#v", bans)
	}
	ban := bans[0].(map[string]any)
	saAssertKeys(t, ban, "uid", "reason", "expiresAt", "createdAt")
	if ban["uid"] != float64(f.outsider) || ban["reason"] != "ban reason" || ban["expiresAt"] != expires.Format(time.RFC3339) {
		t.Fatalf("export ban = %#v", ban)
	}
	events := server["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("export events = %#v", events)
	}
	event := events[0].(map[string]any)
	saAssertKeys(t, event, "id", "createdAt", "outcome", "reasonCode", "durationMs", "sourceMode")
	if event["outcome"] != "allow" || event["reasonCode"] != "local_session" || event["durationMs"] != float64(17) || event["sourceMode"] != "url_secret" {
		t.Fatalf("export event = %#v", event)
	}
	credentials := server["credentials"].([]any)
	if len(credentials) != 2 {
		t.Fatalf("export credentials = %#v", credentials)
	}
	for _, raw := range credentials {
		credential := raw.(map[string]any)
		saAssertKeys(t, credential, "id", "mode", "status", "createdAt", "lastUsedAt", "expiresAt")
		switch credential["id"] {
		case float64(activeID):
			if credential["mode"] != "url_secret" || credential["status"] != "active" || credential["lastUsedAt"] != f.now.Format(time.RFC3339) || credential["expiresAt"] != nil {
				t.Fatalf("export active credential = %#v", credential)
			}
		case float64(revokedID):
			if credential["mode"] != "header" || credential["status"] != "revoked" || credential["lastUsedAt"] != nil || credential["expiresAt"] != expires.Format(time.RFC3339) {
				t.Fatalf("export revoked credential = %#v", credential)
			}
		default:
			t.Fatalf("unexpected export credential = %#v", credential)
		}
	}
	for _, forbidden := range []string{
		"owner@example.com", "admin@example.com", "invitee@example.com", "203.0.113.10",
		"GlobalPlayer", "PrivatePlayer", playerUUID, textureHash, activeHash, revokedHash, strings.Repeat("f", 64),
		"secretHash", "private:scope", "invites", "requestId", "playerName", "joinSessionId", "srv_two",
	} {
		if strings.Contains(rec.Body.String(), forbidden) {
			t.Fatalf("export leaked %q: %s", forbidden, rec.Body.String())
		}
	}
	if playerID <= 0 || textureID <= 0 {
		t.Fatal("global identity fixtures were not created")
	}
	if detail := saAuditDetail(t, f, "serveraccess_tenant_export"); detail != fmt.Sprintf(`{"tenantId":%d}`, f.tenant) {
		t.Fatalf("export audit detail = %s, want tenant id only", detail)
	}

	for _, tc := range []struct {
		name string
		uid  int64
		want int
	}{
		{"anonymous", 0, http.StatusUnauthorized},
		{"non-member", f.legacyAdmin, http.StatusForbidden},
		{"viewer", f.viewer, http.StatusForbidden},
		{"admin", f.admin, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := f.request(t, tc.uid, http.MethodGet, "/api/serveraccess/v1/tenants/me/export", "", "")
			if got.Code != tc.want {
				t.Fatalf("export as %s = %d %s, want %d", tc.name, got.Code, got.Body.String(), tc.want)
			}
		})
	}
}

func TestServerAccessAPITenantExportRowLimitsReturnExplicit413(t *testing.T) {
	for _, tc := range []struct {
		name   string
		limits store.SAExportLimits
		seed   func(*testing.T, *saFixture)
	}{
		{"servers", store.SAExportLimits{MaxServers: 1}, func(t *testing.T, f *saFixture) {
			if _, err := f.st.CreateSAServer(context.Background(), f.tenant, "srv_export_over", "over"); err != nil {
				t.Fatal(err)
			}
		}},
		{"whitelist", store.SAExportLimits{MaxWhitelist: 1}, func(t *testing.T, f *saFixture) {
			for i := int64(0); i < 2; i++ {
				if err := f.st.AddSAServerWhitelist(context.Background(), f.tenant, f.server, 1000+i, "note", f.owner); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"bans", store.SAExportLimits{MaxBans: 1}, func(t *testing.T, f *saFixture) {
			for i := int64(0); i < 2; i++ {
				if err := f.st.AddSAServerBan(context.Background(), f.tenant, f.server, 1000+i, "reason", sql.NullTime{}, f.owner); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"events", store.SAExportLimits{MaxEvents: 1}, func(t *testing.T, f *saFixture) {
			for i := 0; i < 2; i++ {
				if err := f.st.InsertSAJoinEvent(context.Background(), store.SAJoinEvent{
					TenantID: f.tenant, ServerID: f.server, Outcome: "allow", ReasonCode: "local_session", SourceMode: "header",
				}); err != nil {
					t.Fatal(err)
				}
			}
		}},
		{"credentials", store.SAExportLimits{MaxCredentials: 1}, func(t *testing.T, f *saFixture) {
			for i := 0; i < 2; i++ {
				if _, err := f.st.CreateSAServerCredential(context.Background(), f.tenant, f.server, store.SACredential{
					Mode: "header", SecretHash: strings.Repeat(string(rune('a'+i)), 64), Scopes: `["verify:join"]`, Status: "active",
				}); err != nil {
					t.Fatal(err)
				}
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSAFixture(t)
			tc.seed(t, f)
			stack := NewStack(Config{Enabled: true, Store: f.st, Now: f.stack.cfg.Now, ExportLimits: tc.limits})
			f.h = stack.Gate(stack.Handler())
			rec := f.request(t, f.owner, http.MethodGet, "/api/serveraccess/v1/tenants/me/export", "", "")
			if rec.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("export over %s limit = %d %s, want 413", tc.name, rec.Code, rec.Body.String())
			}
			var body struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
			if body.Code != "serveraccess.export_too_large" || !strings.Contains(body.Message, tc.name) || !strings.Contains(body.Message, "limit") {
				t.Fatalf("413 body = %s, want explicit collection and limit", rec.Body.String())
			}
			if got := rec.Header().Get("Content-Disposition"); got != "" {
				t.Fatalf("413 Content-Disposition = %q, want no attachment", got)
			}
			var audits int
			if err := f.st.DB().QueryRowContext(context.Background(), `SELECT COUNT(*) FROM audit_log WHERE action='serveraccess_tenant_export'`).Scan(&audits); err != nil {
				t.Fatal(err)
			}
			if audits != 0 {
				t.Fatalf("rejected export audits = %d, want 0", audits)
			}
		})
	}
}

func TestServerAccessAPITenantExportByteLimitExactBoundary(t *testing.T) {
	f := newSAFixture(t)
	path := "/api/serveraccess/v1/tenants/me/export"
	baseline := f.request(t, f.owner, http.MethodGet, path, "", "")
	if baseline.Code != http.StatusOK {
		t.Fatalf("baseline export = %d %s", baseline.Code, baseline.Body.String())
	}
	for _, tc := range []struct {
		name string
		max  int
		want int
	}{
		{"exact", baseline.Body.Len(), http.StatusOK},
		{"one byte over", baseline.Body.Len() - 1, http.StatusRequestEntityTooLarge},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stack := NewStack(Config{Enabled: true, Store: f.st, Now: f.stack.cfg.Now, ExportLimits: store.SAExportLimits{MaxBytes: tc.max}})
			f.h = stack.Gate(stack.Handler())
			rec := f.request(t, f.owner, http.MethodGet, path, "", "")
			if rec.Code != tc.want {
				t.Fatalf("export byte limit %d = %d %s, want %d", tc.max, rec.Code, rec.Body.String(), tc.want)
			}
			if tc.want == http.StatusRequestEntityTooLarge {
				if !strings.Contains(rec.Body.String(), "bytes") || rec.Header().Get("Content-Disposition") != "" {
					t.Fatalf("byte overflow response = %s headers=%v", rec.Body.String(), rec.Header())
				}
			}
		})
	}
}

func TestServerAccessAPITenantExportBatchesMultipleServersWithoutMixingRows(t *testing.T) {
	f := newSAFixture(t)
	ctx := context.Background()
	second, err := f.st.CreateSAServer(ctx, f.tenant, "srv_export_second", "second")
	if err != nil {
		t.Fatal(err)
	}
	for i, serverID := range []int64{f.server, second.ID} {
		uid := int64(2000 + i)
		if err := f.st.AddSAServerWhitelist(ctx, f.tenant, serverID, uid, fmt.Sprintf("note-%d", i), f.owner); err != nil {
			t.Fatal(err)
		}
		if err := f.st.AddSAServerBan(ctx, f.tenant, serverID, uid, fmt.Sprintf("reason-%d", i), sql.NullTime{}, f.owner); err != nil {
			t.Fatal(err)
		}
		if _, err := f.st.CreateSAServerCredential(ctx, f.tenant, serverID, store.SACredential{
			Mode: "header", SecretHash: strings.Repeat(string(rune('c'+i)), 64), Scopes: `["verify:join"]`, Status: "active",
		}); err != nil {
			t.Fatal(err)
		}
		if err := f.st.InsertSAJoinEvent(ctx, store.SAJoinEvent{
			TenantID: f.tenant, ServerID: serverID, Outcome: "allow", ReasonCode: fmt.Sprintf("reason-%d", i), SourceMode: "header",
			PlayerName: "PrivatePlayer", UUID: "private-uuid", RequestID: "private-request",
		}); err != nil {
			t.Fatal(err)
		}
	}
	rec := f.request(t, f.owner, http.MethodGet, "/api/serveraccess/v1/tenants/me/export", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("multi-server export = %d %s", rec.Code, rec.Body.String())
	}
	var document struct {
		Servers []struct {
			ID        int64 `json:"id"`
			Whitelist []struct {
				UID int64 `json:"uid"`
			} `json:"whitelist"`
			Bans []struct {
				Reason string `json:"reason"`
			} `json:"bans"`
			Events []struct {
				ReasonCode string `json:"reasonCode"`
			} `json:"events"`
			Credentials []struct {
				ID int64 `json:"id"`
			} `json:"credentials"`
		} `json:"servers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if len(document.Servers) != 2 {
		t.Fatalf("servers = %+v, want 2", document.Servers)
	}
	for _, server := range document.Servers {
		i := int64(0)
		if server.ID == second.ID {
			i = 1
		} else if server.ID != f.server {
			t.Fatalf("unexpected export server %d", server.ID)
		}
		if len(server.Whitelist) != 1 || len(server.Bans) != 1 || len(server.Events) != 1 || len(server.Credentials) != 1 {
			t.Fatalf("server %d rows = %+v, want one of each", server.ID, server)
		}
		if server.Whitelist[0].UID != int64(2000+i) || server.Bans[0].Reason != fmt.Sprintf("reason-%d", i) || server.Events[0].ReasonCode != fmt.Sprintf("reason-%d", i) {
			t.Fatalf("server %d rows mixed: %+v", server.ID, server)
		}
	}
	for _, forbidden := range []string{"PrivatePlayer", "private-uuid", "private-request", "playerName", "requestId", "secretHash", "scopes"} {
		if strings.Contains(rec.Body.String(), forbidden) {
			t.Fatalf("multi-server export leaked %q", forbidden)
		}
	}
}

func TestServerAccessAPIDeleteRequestAtomicallySuspendsAndRevokes(t *testing.T) {
	f := newSAFixture(t)
	ctx := context.Background()
	hash := strings.Repeat("a", 64)
	credentialID, err := f.st.CreateSAServerCredential(ctx, f.tenant, f.server, store.SACredential{
		Mode: "header", SecretHash: hash, Scopes: `["verify:join"]`, Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/serveraccess/v1/tenants/me/delete-request?tenant_id=" + fmt.Sprint(f.otherTenant)
	rec := f.request(t, f.owner, http.MethodPost, path, `{}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete request = %d %s, want 200", rec.Code, rec.Body.String())
	}
	var status string
	var requested, due sql.NullTime
	if err := f.st.DB().QueryRowContext(ctx,
		`SELECT status,deletion_requested_at,deletion_due_at FROM sa_tenants WHERE id=?`, f.tenant).Scan(&status, &requested, &due); err != nil {
		t.Fatal(err)
	}
	if status != "deleting" || !requested.Valid || !due.Valid || !requested.Time.Equal(f.now) || !due.Time.Equal(f.now.Add(7*24*time.Hour)) {
		t.Fatalf("deletion state = status %q requested %+v due %+v", status, requested, due)
	}
	server, err := f.st.SAServerByID(ctx, f.tenant, f.server)
	if err != nil || server.Status != "suspended" {
		t.Fatalf("server after delete request = %+v %v", server, err)
	}
	credentials, err := f.st.ListSAServerCredentials(ctx, f.tenant, f.server)
	if err != nil || len(credentials) != 1 || credentials[0].ID != credentialID || credentials[0].Status != "revoked" {
		t.Fatalf("credentials after delete request = %+v %v", credentials, err)
	}
	if _, err := f.st.SACredentialBySecretHash(ctx, hash, f.now); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("credential still resolves after delete request: %v", err)
	}
	detail := saAuditDetail(t, f, "serveraccess_tenant_delete_request")
	var audit map[string]any
	if err := json.Unmarshal([]byte(detail), &audit); err != nil {
		t.Fatalf("decode delete request audit: %v", err)
	}
	wantAudit := map[string]any{"tenantId": float64(f.tenant), "deletionDueAt": f.now.Add(7 * 24 * time.Hour).Format(time.RFC3339)}
	if !reflect.DeepEqual(audit, wantAudit) {
		t.Fatalf("delete request audit detail = %s, want %#v", detail, wantAudit)
	}
	rec = f.request(t, f.owner, http.MethodGet, "/api/serveraccess/v1/tenants/me/export", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("owner export during deletion grace = %d %s, want 200", rec.Code, rec.Body.String())
	}
	var document saExportDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode grace export: %v", err)
	}
	if document.Tenant.Status != "deleting" || len(document.Servers) != 1 || document.Servers[0].Status != "suspended" {
		t.Fatalf("grace export lost deletion state: %+v", document)
	}
	rec = f.request(t, f.viewer, http.MethodGet, "/api/serveraccess/v1/tenants/me/export", "", "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("viewer export during deletion grace = %d %s, want 403", rec.Code, rec.Body.String())
	}
}

func TestServerAccessAPILifecycleExportPrefersActiveMembership(t *testing.T) {
	f := newSAFixture(t)
	ctx := context.Background()
	uid := saUser(t, f.st, "active-after-deleting@example.com", "active_after_deleting", 0)
	if err := f.st.AddSATenantMember(ctx, f.otherTenant, uid, "viewer"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.DB().ExecContext(ctx,
		`UPDATE sa_tenant_members SET created_at=? WHERE tenant_id=? AND uid=?`,
		f.now.Add(-time.Hour), f.otherTenant, uid); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.RequestSATenantDeletion(ctx, f.otherTenant, 7*24*time.Hour, f.now); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AddSATenantMember(ctx, f.tenant, uid, "admin"); err != nil {
		t.Fatal(err)
	}
	rec := f.request(t, uid, http.MethodGet, "/api/serveraccess/v1/tenants/me/export", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("export with active and deleting memberships = %d %s, want active tenant", rec.Code, rec.Body.String())
	}
	var document saExportDocument
	if err := json.Unmarshal(rec.Body.Bytes(), &document); err != nil {
		t.Fatal(err)
	}
	if document.Tenant.ID != f.tenant || document.Tenant.Status != "active" {
		t.Fatalf("export selected wrong membership: %+v", document.Tenant)
	}
}

func TestServerAccessAPIDeleteRequestRoleAndOriginGates(t *testing.T) {
	for _, tc := range []struct {
		name   string
		uid    int64
		body   string
		origin string
		want   int
	}{
		{"anonymous", 0, `{}`, "http://example.com", http.StatusUnauthorized},
		{"non-member", 0, `{}`, "http://example.com", http.StatusForbidden},
		{"viewer", 0, `{}`, "http://example.com", http.StatusForbidden},
		{"admin", 0, `{}`, "http://example.com", http.StatusForbidden},
		{"unknown tenant field", 0, `{"tenant_id":2}`, "http://example.com", http.StatusBadRequest},
		{"cross origin", 0, `{}`, "https://evil.example", http.StatusForbidden},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newSAFixture(t)
			uid := tc.uid
			if tc.name == "non-member" {
				uid = f.legacyAdmin
			} else if tc.name == "viewer" {
				uid = f.viewer
			} else if tc.name == "admin" {
				uid = f.admin
			} else if tc.name == "unknown tenant field" || tc.name == "cross origin" {
				uid = f.owner
			}
			rec := f.requestHeaders(t, uid, http.MethodPost, "/api/serveraccess/v1/tenants/me/delete-request", tc.body, "", map[string]string{"Origin": tc.origin})
			if rec.Code != tc.want {
				t.Fatalf("delete request as %s = %d %s, want %d", tc.name, rec.Code, rec.Body.String(), tc.want)
			}
			tenant, err := f.st.SATenantByID(context.Background(), f.tenant)
			if err != nil || tenant.Status != "active" {
				t.Fatalf("rejected delete request changed tenant: %+v %v", tenant, err)
			}
		})
	}
}

func TestServerAccessAPIDeleteRequestRollsBackOnCredentialFailure(t *testing.T) {
	f := newSAFixture(t)
	ctx := context.Background()
	if _, err := f.st.CreateSAServerCredential(ctx, f.tenant, f.server, store.SACredential{
		Mode: "header", SecretHash: strings.Repeat("a", 64), Scopes: `["verify:join"]`, Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.DB().ExecContext(ctx, `CREATE TRIGGER lifecycle_fail_credential_status
		BEFORE UPDATE OF status ON sa_server_credentials WHEN NEW.status='revoked'
		BEGIN SELECT RAISE(ABORT,'forced credential revoke failure'); END`); err != nil {
		t.Fatal(err)
	}
	rec := f.request(t, f.owner, http.MethodPost, "/api/serveraccess/v1/tenants/me/delete-request", `{}`, "")
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("delete request with revoke failure = %d %s, want 500", rec.Code, rec.Body.String())
	}
	var status string
	var requested, due sql.NullTime
	if err := f.st.DB().QueryRowContext(ctx,
		`SELECT status,deletion_requested_at,deletion_due_at FROM sa_tenants WHERE id=?`, f.tenant).Scan(&status, &requested, &due); err != nil {
		t.Fatal(err)
	}
	server, serverErr := f.st.SAServerByID(ctx, f.tenant, f.server)
	credentials, credentialErr := f.st.ListSAServerCredentials(ctx, f.tenant, f.server)
	if status != "active" || requested.Valid || due.Valid || serverErr != nil || server.Status != "active" || credentialErr != nil || len(credentials) != 1 || credentials[0].Status != "active" {
		t.Fatalf("partial deletion state: tenant=%q requested=%+v due=%+v server=%+v credentials=%+v", status, requested, due, server, credentials)
	}
}

func TestServerAccessAPIPlatformDeletionEnforcesGraceAndPreservesGlobalIdentity(t *testing.T) {
	f := newSAFixture(t)
	ctx := context.Background()
	playerID, playerUUID, err := f.st.CreatePlayer(ctx, f.owner, "GlobalPlayer")
	if err != nil {
		t.Fatal(err)
	}
	textureHash := strings.Repeat("c", 64)
	textureID, err := f.st.AddTexture(ctx, f.owner, "skin", textureHash, "default", store.TextureMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CreateSAServerCredential(ctx, f.tenant, f.server, store.SACredential{
		Mode: "url_secret", SecretHash: strings.Repeat("d", 64), Scopes: `["verify:join"]`, Status: "active",
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AddSAServerWhitelist(ctx, f.tenant, f.server, f.owner, "note", f.owner); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AddSAServerBan(ctx, f.tenant, f.server, f.owner, "reason", sql.NullTime{}, f.owner); err != nil {
		t.Fatal(err)
	}
	if _, err := f.st.CreateSATenantInvite(ctx, f.tenant, "invitee@example.com", "viewer", strings.Repeat("e", 64), f.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := f.st.InsertSAJoinEvent(ctx, store.SAJoinEvent{
		TenantID: f.tenant, ServerID: f.server, UUID: playerUUID, PlayerName: "GlobalPlayer",
		Outcome: "allow", ReasonCode: "local_session", SourceMode: "url_secret", RequestID: "request",
	}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.st.AdmitSAQuota(ctx, f.server, f.now, "verify", 10, f.now); err != nil {
		t.Fatal(err)
	}
	if rec := f.request(t, f.owner, http.MethodPost, "/api/serveraccess/v1/tenants/me/delete-request", `{}`, ""); rec.Code != http.StatusOK {
		t.Fatalf("delete request = %d %s", rec.Code, rec.Body.String())
	}
	path := fmt.Sprintf("/api/admin/v1/serveraccess/tenants/%d/delete", f.tenant)
	if rec := f.request(t, f.super, http.MethodPost, path, `{}`, ""); rec.Code != http.StatusConflict {
		t.Fatalf("early platform delete = %d %s, want 409", rec.Code, rec.Body.String())
	}
	if _, err := f.st.SATenantByID(ctx, f.tenant); err != nil {
		t.Fatalf("early delete removed tenant: %v", err)
	}
	for _, tc := range []struct {
		name  string
		uid   int64
		token string
	}{
		{"anonymous", 0, ""},
		{"owner", f.owner, ""},
		{"legacy admin", f.legacyAdmin, ""},
		{"machine token", 0, "machine-secret"},
	} {
		if rec := f.request(t, tc.uid, http.MethodPost, path, `{}`, tc.token); rec.Code != http.StatusForbidden {
			t.Fatalf("platform delete as %s = %d %s, want 403", tc.name, rec.Code, rec.Body.String())
		}
	}
	*f.clock = f.now.Add(7 * 24 * time.Hour)
	if rec := f.request(t, f.super, http.MethodPost, path, `{}`, ""); rec.Code != http.StatusOK {
		t.Fatalf("due platform delete = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if rec := f.request(t, f.super, http.MethodPost, path, `{}`, ""); rec.Code != http.StatusNotFound {
		t.Fatalf("repeated platform delete = %d %s, want 404", rec.Code, rec.Body.String())
	}
	if _, err := f.st.SATenantByID(ctx, f.tenant); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("deleted tenant lookup = %v, want not found", err)
	}
	for _, query := range []struct {
		table string
		where string
		arg   any
	}{
		{"sa_tenants", "id=?", f.tenant},
		{"sa_tenant_members", "tenant_id=?", f.tenant},
		{"sa_tenant_invites", "tenant_id=?", f.tenant},
		{"sa_servers", "tenant_id=?", f.tenant},
		{"sa_server_policies", "server_id=?", f.server},
		{"sa_server_credentials", "server_id=?", f.server},
		{"sa_server_whitelist", "server_id=?", f.server},
		{"sa_server_bans", "server_id=?", f.server},
		{"sa_join_events", "tenant_id=?", f.tenant},
		{"sa_quota_usage", "server_id=?", f.server},
	} {
		var count int
		if err := f.st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM `+query.table+` WHERE `+query.where, query.arg).Scan(&count); err != nil || count != 0 {
			t.Errorf("%s rows after tenant delete = %d, err %v; want 0", query.table, count, err)
		}
	}
	if _, err := f.st.SAServerByID(ctx, f.otherTenant, f.otherServer); err != nil {
		t.Fatalf("other tenant server was removed: %v", err)
	}
	if _, err := f.st.UserByID(ctx, f.owner); err != nil {
		t.Fatalf("global user was removed: %v", err)
	}
	if _, err := f.st.PlayerByID(ctx, playerID); err != nil {
		t.Fatalf("global player was removed: %v", err)
	}
	if hash, _, _, err := f.st.TextureByTID(ctx, textureID); err != nil || hash != textureHash {
		t.Fatalf("global texture after tenant delete = %q %v", hash, err)
	}
	if detail := saAuditDetail(t, f, "serveraccess_tenant_delete"); detail != fmt.Sprintf(`{"tenantId":%d}`, f.tenant) {
		t.Fatalf("platform delete audit detail = %s", detail)
	}
	otherPath := fmt.Sprintf("/api/admin/v1/serveraccess/tenants/%d/delete", f.otherTenant)
	if rec := f.request(t, f.super, http.MethodPost, otherPath, `{}`, ""); rec.Code != http.StatusConflict {
		t.Fatalf("platform delete without request = %d %s, want 409", rec.Code, rec.Body.String())
	}
	if _, err := f.st.SATenantByID(ctx, f.otherTenant); err != nil {
		t.Fatalf("non-deleting tenant was removed: %v", err)
	}
}

func TestServerAccessAPIDeleteRequestUsesConfiguredGrace(t *testing.T) {
	f := newSAFixture(t)
	cfg := Config{Enabled: true, Store: f.st, Now: f.stack.cfg.Now, DeletionGracePeriod: 48 * time.Hour}
	stack := NewStack(cfg)
	f.h = stack.Gate(stack.Handler())
	rec := f.request(t, f.owner, http.MethodPost, "/api/serveraccess/v1/tenants/me/delete-request", `{}`, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("delete request = %d %s, want 200", rec.Code, rec.Body.String())
	}
	var due sql.NullTime
	if err := f.st.DB().QueryRowContext(context.Background(), `SELECT deletion_due_at FROM sa_tenants WHERE id=?`, f.tenant).Scan(&due); err != nil {
		t.Fatal(err)
	}
	if !due.Valid || !due.Time.Equal(f.now.Add(48*time.Hour)) {
		t.Fatalf("configured deletion due = %+v, want %s", due, f.now.Add(48*time.Hour).Format(time.RFC3339))
	}
}

func TestServerAccessAPIDeletingTenantCannotBeReactivated(t *testing.T) {
	f := newSAFixture(t)
	if rec := f.request(t, f.owner, http.MethodPost, "/api/serveraccess/v1/tenants/me/delete-request", `{}`, ""); rec.Code != http.StatusOK {
		t.Fatalf("delete request = %d %s", rec.Code, rec.Body.String())
	}
	path := fmt.Sprintf("/api/admin/v1/serveraccess/tenants/%d", f.tenant)
	rec := f.request(t, f.super, http.MethodPatch, path, `{"status":"active"}`, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("reactivate deleting tenant = %d %s, want 409", rec.Code, rec.Body.String())
	}
	tenant, err := f.st.SATenantByID(context.Background(), f.tenant)
	if err != nil || tenant.Status != "deleting" || tenant.DeletionRequestedAt == nil || tenant.DeletionDueAt == nil {
		t.Fatalf("deleting tenant was reactivated: %+v %v", tenant, err)
	}
}

func TestServerAccessAPIDeletingTenantServersCannotBeReactivated(t *testing.T) {
	f := newSAFixture(t)
	if rec := f.request(t, f.owner, http.MethodPost, "/api/serveraccess/v1/tenants/me/delete-request", `{}`, ""); rec.Code != http.StatusOK {
		t.Fatalf("delete request = %d %s", rec.Code, rec.Body.String())
	}
	path := fmt.Sprintf("/api/admin/v1/serveraccess/servers/%d", f.server)
	rec := f.request(t, f.super, http.MethodPatch, path, `{"status":"active"}`, "")
	if rec.Code != http.StatusConflict {
		t.Fatalf("reactivate deleting tenant server = %d %s, want 409", rec.Code, rec.Body.String())
	}
	server, err := f.st.SAServerByID(context.Background(), f.tenant, f.server)
	if err != nil || server.Status != "suspended" {
		t.Fatalf("deleting tenant server was reactivated: %+v %v", server, err)
	}
}

func TestServerAccessAPILifecycleRoutesRespectDisabledGate(t *testing.T) {
	f := newSAFixture(t)
	f.stack.SetEnabled(false)
	for _, tc := range []struct {
		method, path string
	}{
		{http.MethodGet, "/api/serveraccess/v1/tenants/me/export"},
		{http.MethodPost, "/api/serveraccess/v1/tenants/me/delete-request"},
		{http.MethodPost, fmt.Sprintf("/api/admin/v1/serveraccess/tenants/%d/delete", f.tenant)},
	} {
		rec := f.request(t, f.owner, tc.method, tc.path, `{}`, "")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("disabled %s %s = %d %s, want 404", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
}
