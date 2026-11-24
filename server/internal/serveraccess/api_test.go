package serveraccess

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"skin-nexus/server/internal/dialect"
	"skin-nexus/server/internal/migrate"
	"skin-nexus/server/internal/store"
)

type saFixture struct {
	stack       *Stack
	h           http.Handler
	st          *store.Mysql
	now         time.Time
	clock       *time.Time
	owner       int64
	admin       int64
	viewer      int64
	outsider    int64
	super       int64
	legacyAdmin int64
	tenant      int64
	otherTenant int64
	server      int64
	otherServer int64
}

func newSAFixture(t *testing.T) *saFixture {
	t.Helper()
	st, err := store.Open("sqlite", filepath.Join(t.TempDir(), "api.db"), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := migrate.ApplySQLDirTracked(st.DB(), filepath.Join("..", "..", "migrations"), dialect.SQLite); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	f := &saFixture{st: st, now: now, clock: &now}
	f.owner = saUser(t, st, "owner@example.com", "owner", 0)
	f.admin = saUser(t, st, "admin@example.com", "admin", 0)
	f.viewer = saUser(t, st, "viewer@example.com", "viewer", 0)
	f.outsider = saUser(t, st, "outsider@example.com", "outsider", 0)
	f.super = saUser(t, st, "super@example.com", "super", 2)
	f.legacyAdmin = saUser(t, st, "legacy@example.com", "legacy", 1)
	f.tenant, _ = st.CreateSATenant(context.Background(), "one", 10)
	f.otherTenant, _ = st.CreateSATenant(context.Background(), "two", 10)
	for uid, role := range map[int64]string{f.owner: "owner", f.admin: "admin", f.viewer: "viewer"} {
		if err := st.AddSATenantMember(context.Background(), f.tenant, uid, role); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.AddSATenantMember(context.Background(), f.otherTenant, f.outsider, "owner"); err != nil {
		t.Fatal(err)
	}
	createdServer, err := st.CreateSAServer(context.Background(), f.tenant, "srv_one", "one")
	if err != nil {
		t.Fatal(err)
	}
	f.server = createdServer.ID
	otherCreated, err := st.CreateSAServer(context.Background(), f.otherTenant, "srv_two", "two")
	if err != nil {
		t.Fatal(err)
	}
	f.otherServer = otherCreated.ID
	s := NewStack(Config{Enabled: true, Store: st, Now: func() time.Time { return *f.clock }})
	f.stack = s
	f.h = s.Gate(s.Handler())
	return f
}

func saUser(t *testing.T, st *store.Mysql, email, nick string, permission int) int64 {
	t.Helper()
	uid, err := st.CreateUser(context.Background(), email, nick, "x", 3, "")
	if err != nil {
		t.Fatal(err)
	}
	if permission != 0 {
		if err := st.SetUserPermission(context.Background(), uid, permission); err != nil {
			t.Fatal(err)
		}
	}
	return uid
}

func (f *saFixture) request(t *testing.T, uid int64, method, path, body string, token string) *httptest.ResponseRecorder {
	t.Helper()
	return f.requestHeaders(t, uid, method, path, body, token, nil)
}

func (f *saFixture) requestHeaders(t *testing.T, uid int64, method, path, body, token string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	if uid > 0 {
		session := fmt.Sprintf("session-%d-%d", uid, time.Now().UnixNano())
		_ = f.st.DeleteSession(context.Background(), session)
		if err := f.st.CreateSession(context.Background(), session, uid, f.now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		r.AddCookie(&http.Cookie{Name: "skin_session", Value: session})
	}
	if token != "" {
		r.Header.Set("X-Admin-Token", token)
	}
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	r.Header.Set("Origin", "http://example.com")
	for name, value := range headers {
		r.Header.Set(name, value)
	}
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, r)
	return rec
}

func TestServerAccessAPIRoleMatrix(t *testing.T) {
	f := newSAFixture(t)
	for _, tc := range []struct {
		name               string
		uid                int64
		method, path, body string
		token              string
		want               int
	}{
		{"anonymous", 0, "GET", "/api/serveraccess/v1/tenants/me", "", "", 401},
		{"non-member", f.legacyAdmin, "GET", "/api/serveraccess/v1/tenants/me", "", "", 403},
		{"viewer reads", f.viewer, "GET", "/api/serveraccess/v1/servers", "", "", 200},
		{"viewer cannot write", f.viewer, "POST", "/api/serveraccess/v1/servers", `{"name":"no"}`, "", 403},
		{"admin writes server", f.admin, "POST", "/api/serveraccess/v1/servers", `{"name":"yes"}`, "", 201},
		{"admin cannot manage members", f.admin, "POST", "/api/serveraccess/v1/tenants/me/members/invite", `{"email":"new@example.com","role":"viewer"}`, "", 403},
		{"owner invites", f.owner, "POST", "/api/serveraccess/v1/tenants/me/members/invite", `{"email":"new@example.com","role":"viewer"}`, "", 201},
		{"platform rejects owner", f.owner, "GET", "/api/admin/v1/serveraccess/tenants", "", "", 403},
		{"platform rejects legacy admin", f.legacyAdmin, "GET", "/api/admin/v1/serveraccess/tenants", "", "", 403},
		{"platform rejects machine token", 0, "GET", "/api/admin/v1/serveraccess/tenants", "", "machine-secret", 403},
		{"platform super session", f.super, "GET", "/api/admin/v1/serveraccess/tenants", "", "", 200},
		{"super session wins over token header", f.super, "GET", "/api/admin/v1/serveraccess/tenants", "", "machine-secret", 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.request(t, tc.uid, tc.method, tc.path, tc.body, tc.token)
			if rec.Code != tc.want {
				t.Fatalf("%s %s = %d %s, want %d", tc.method, tc.path, rec.Code, rec.Body.String(), tc.want)
			}
		})
	}
}

func TestServerAccessAPIRejectsCrossOriginMutations(t *testing.T) {
	f := newSAFixture(t)
	cred, err := f.st.CreateSAServerCredential(context.Background(), f.tenant, f.server, store.SACredential{
		Mode: "header", SecretHash: strings.Repeat("a", 64), Scopes: `["verify:join"]`, Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	invite, err := f.st.CreateSATenantInvite(context.Background(), f.tenant, "new@example.com", "viewer", strings.Repeat("b", 64), f.now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/serveraccess/v1/servers/%d", f.server)
	for _, tc := range []struct {
		name         string
		uid          int64
		method, path string
		body         string
	}{
		{"owner create", f.owner, "POST", "/api/serveraccess/v1/servers", `{"name":"cross-origin"}`},
		{"owner update", f.owner, "PATCH", base, `{"name":"cross-origin"}`},
		{"owner invite", f.owner, "POST", "/api/serveraccess/v1/tenants/me/members/invite", `{"email":"new@example.com","role":"viewer"}`},
		{"owner revoke invite", f.owner, "DELETE", fmt.Sprintf("/api/serveraccess/v1/tenants/me/invites/%d", invite), ""},
		{"owner remove member", f.owner, "DELETE", fmt.Sprintf("/api/serveraccess/v1/tenants/me/members/%d", f.admin), ""},
		{"owner whitelist add", f.owner, "POST", base + "/whitelist", `{"uid":1}`},
		{"owner whitelist remove", f.owner, "DELETE", base + "/whitelist/1", ""},
		{"owner ban add", f.owner, "POST", base + "/bans", `{"uid":1}`},
		{"owner ban remove", f.owner, "DELETE", base + "/bans/1", ""},
		{"owner credential create", f.owner, "POST", base + "/credentials", `{"mode":"header"}`},
		{"owner credential rotate", f.owner, "POST", fmt.Sprintf("%s/credentials/%d/rotate", base, cred), `{}`},
		{"owner credential revoke", f.owner, "POST", fmt.Sprintf("%s/credentials/%d/revoke", base, cred), `{}`},
		{"platform tenant update", f.super, "PATCH", fmt.Sprintf("/api/admin/v1/serveraccess/tenants/%d", f.tenant), `{"serverLimit":2}`},
		{"platform server update", f.super, "PATCH", fmt.Sprintf("/api/admin/v1/serveraccess/servers/%d", f.server), `{"status":"disabled"}`},
		{"platform credential revoke", f.super, "POST", fmt.Sprintf("/api/admin/v1/serveraccess/servers/%d/credentials/%d/revoke", f.server, cred), `{}`},
		{"platform fallback", f.super, "PUT", "/api/admin/v1/serveraccess/fallback", `{"enabled":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := f.requestHeaders(t, tc.uid, tc.method, tc.path, tc.body, "", map[string]string{
				"Origin":         "https://evil.example",
				"Sec-Fetch-Site": "same-origin",
			})
			if rec.Code != http.StatusForbidden {
				t.Fatalf("cross-origin mutation = %d %s, want 403", rec.Code, rec.Body.String())
			}
		})
	}
	server, err := f.st.SAServerByID(context.Background(), f.tenant, f.server)
	if err != nil || server.Name != "one" || server.Status != "active" {
		t.Fatalf("cross-origin update changed server: %+v %v", server, err)
	}
	tenant, err := f.st.SATenantByID(context.Background(), f.tenant)
	if err != nil || tenant.ServerLimit != 10 {
		t.Fatalf("cross-origin update changed tenant: %+v %v", tenant, err)
	}
	if _, err := f.st.SACredentialBySecretHash(context.Background(), strings.Repeat("a", 64), f.now); err != nil {
		t.Fatalf("cross-origin request revoked credential: %v", err)
	}
}

func TestServerAccessAPIJSONBodyRequiresJSONContentTypeOrCustomHeader(t *testing.T) {
	f := newSAFixture(t)
	for _, tc := range []struct {
		name, contentType, requestedWith string
		want                             int
	}{
		{"missing content type", "", "", http.StatusUnsupportedMediaType},
		{"text plain", "text/plain", "", http.StatusUnsupportedMediaType},
		{"form encoded", "application/x-www-form-urlencoded", "", http.StatusUnsupportedMediaType},
		{"wrong custom header", "text/plain", "wrong", http.StatusUnsupportedMediaType},
		{"json", "application/json", "", http.StatusCreated},
		{"json charset", "application/json; charset=utf-8", "", http.StatusCreated},
		{"custom header", "text/plain", "XMLHttpRequest", http.StatusCreated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string]string{}
			if tc.contentType != "" {
				headers["Content-Type"] = tc.contentType
			} else {
				headers["Content-Type"] = ""
			}
			if tc.requestedWith != "" {
				headers["X-Requested-With"] = tc.requestedWith
			}
			rec := f.requestHeaders(t, f.owner, "POST", "/api/serveraccess/v1/servers", `{"name":"json-gate"}`, "", headers)
			if rec.Code != tc.want {
				t.Fatalf("content type %q custom header %q = %d %s, want %d", tc.contentType, tc.requestedWith, rec.Code, rec.Body.String(), tc.want)
			}
		})
	}
}

func TestServerAccessAPICrossTenantEveryFamilyReturns404(t *testing.T) {
	f := newSAFixture(t)
	cred, err := f.st.CreateSAServerCredential(context.Background(), f.otherTenant, f.otherServer, store.SACredential{Mode: "url_secret", SecretHash: strings.Repeat("b", 64), Scopes: `["verify:join"]`, Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	invite, err := f.st.CreateSATenantInvite(context.Background(), f.otherTenant, "other@example.com", "viewer", strings.Repeat("c", 64), f.now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.AddSAServerWhitelist(context.Background(), f.otherTenant, f.otherServer, f.owner, "x", f.outsider); err != nil {
		t.Fatal(err)
	}
	if err := f.st.AddSAServerBan(context.Background(), f.otherTenant, f.otherServer, f.owner, "x", sql.NullTime{}, f.outsider); err != nil {
		t.Fatal(err)
	}
	base := fmt.Sprintf("/api/serveraccess/v1/servers/%d", f.otherServer)
	for _, tc := range []struct{ method, path, body string }{
		{"DELETE", fmt.Sprintf("/api/serveraccess/v1/tenants/me/members/%d", f.outsider), ""},
		{"DELETE", fmt.Sprintf("/api/serveraccess/v1/tenants/me/invites/%d", invite), ""},
		{"GET", base, ""},
		{"PATCH", base, `{"name":"stolen"}`},
		{"GET", base + "/whitelist", ""},
		{"POST", base + "/whitelist", `{"uid":1}`},
		{"DELETE", base + "/whitelist/1", ""},
		{"GET", base + "/bans", ""},
		{"POST", base + "/bans", `{"uid":1}`},
		{"DELETE", base + "/bans/1", ""},
		{"GET", base + "/credentials", ""},
		{"POST", base + "/credentials", `{"mode":"url_secret"}`},
		{"POST", fmt.Sprintf("%s/credentials/%d/rotate", base, cred), `{}`},
		{"POST", fmt.Sprintf("%s/credentials/%d/revoke", base, cred), `{}`},
		{"GET", base + "/export", ""},
	} {
		rec := f.request(t, f.owner, tc.method, tc.path, tc.body, "")
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d %s, want 404", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
	rec := f.request(t, f.owner, "GET", "/api/serveraccess/v1/tenants/me?tenant_id="+fmt.Sprint(f.otherTenant), "", "")
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), fmt.Sprintf(`"tenantId":%d`, f.tenant)) || strings.Contains(rec.Body.String(), fmt.Sprintf(`"tenantId":%d`, f.otherTenant)) {
		t.Fatalf("query tenant_id influenced derived tenant: %d %s", rec.Code, rec.Body.String())
	}
	rec = f.request(t, f.owner, "POST", "/api/serveraccess/v1/servers", `{"name":"evil","tenant_id":`+fmt.Sprint(f.otherTenant)+`}`, "")
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("body tenant_id = %d %s, want 400", rec.Code, rec.Body.String())
	}
}

func TestServerAccessAPIInviteAcceptanceAndCredentialOneTimeRotation(t *testing.T) {
	f := newSAFixture(t)
	invitee := saUser(t, f.st, "invitee@example.com", "invitee", 0)
	invite := f.request(t, f.owner, "POST", "/api/serveraccess/v1/tenants/me/members/invite", `{"email":"invitee@example.com","role":"viewer"}`, "")
	if invite.Code != http.StatusCreated {
		t.Fatalf("invite = %d %s", invite.Code, invite.Body.String())
	}
	var inviteBody struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(invite.Body.Bytes(), &inviteBody); err != nil || inviteBody.Token == "" {
		t.Fatalf("invite token missing: %s %v", invite.Body.String(), err)
	}
	accept := func() *httptest.ResponseRecorder {
		b, _ := json.Marshal(map[string]string{"token": inviteBody.Token})
		return f.request(t, invitee, "POST", "/api/serveraccess/v1/invites/accept", string(b), "")
	}
	if rec := accept(); rec.Code != http.StatusOK {
		t.Fatalf("accept = %d %s", rec.Code, rec.Body.String())
	}
	if rec := accept(); rec.Code != http.StatusNotFound {
		t.Fatalf("replay = %d %s, want 404", rec.Code, rec.Body.String())
	}
	base := fmt.Sprintf("/api/serveraccess/v1/servers/%d/credentials", f.server)
	create := f.request(t, f.owner, "POST", base, `{"mode":"url_secret"}`, "")
	if create.Code != http.StatusCreated {
		t.Fatalf("create credential = %d %s", create.Code, create.Body.String())
	}
	var created struct {
		Secret     string           `json:"secret"`
		Credential saCredentialView `json:"credential"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil || len(created.Secret) < 43 || created.Credential.ID == 0 || created.Credential.ServerID != f.server {
		t.Fatalf("create body = %s %v", create.Body.String(), err)
	}
	list := f.request(t, f.owner, "GET", base, "", "")
	if list.Code != http.StatusOK || strings.Contains(list.Body.String(), created.Secret) || strings.Contains(list.Body.String(), "secretHash") {
		t.Fatalf("list leaked secret = %d %s", list.Code, list.Body.String())
	}
	rotate := f.request(t, f.owner, "POST", fmt.Sprintf("%s/%d/rotate", base, created.Credential.ID), `{}`, "")
	if rotate.Code != http.StatusCreated || !strings.Contains(rotate.Body.String(), `"secret"`) {
		t.Fatalf("rotate = %d %s", rotate.Code, rotate.Body.String())
	}
	revoke := f.request(t, f.owner, "POST", fmt.Sprintf("%s/%d/revoke", base, created.Credential.ID), `{}`, "")
	if revoke.Code != http.StatusOK {
		t.Fatalf("revoke old = %d %s", revoke.Code, revoke.Body.String())
	}
	var rotated struct {
		Credential saCredentialView `json:"credential"`
	}
	if err := json.Unmarshal(rotate.Body.Bytes(), &rotated); err != nil {
		t.Fatal(err)
	}
	revokeNew := f.request(t, f.owner, "POST", fmt.Sprintf("%s/%d/revoke", base, rotated.Credential.ID), `{}`, "")
	if revokeNew.Code != http.StatusOK {
		t.Fatalf("revoke new = %d %s", revokeNew.Code, revokeNew.Body.String())
	}
}

func TestServerAccessAPIInviteCannotCreateSecondActiveMembership(t *testing.T) {
	f := newSAFixture(t)
	hash := saHash("second-active-membership")
	if _, err := f.st.CreateSATenantInvite(context.Background(), f.otherTenant, "owner@example.com", "viewer", hash, f.now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	rec := f.request(t, f.owner, "POST", "/api/serveraccess/v1/invites/accept", `{"token":"second-active-membership"}`, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("second active membership accept = %d %s, want 403", rec.Code, rec.Body.String())
	}
	me := f.request(t, f.owner, "GET", "/api/serveraccess/v1/tenants/me", "", "")
	if me.Code != http.StatusOK || !strings.Contains(me.Body.String(), fmt.Sprintf(`"tenantId":%d`, f.tenant)) ||
		strings.Contains(me.Body.String(), fmt.Sprintf(`"tenantId":%d`, f.otherTenant)) {
		t.Fatalf("portal tenant became ambiguous: %d %s", me.Code, me.Body.String())
	}
}

func TestServerAccessAPIPlatformUpdatesFallbackAndAudit(t *testing.T) {
	f := newSAFixture(t)
	for _, tc := range []struct {
		method, path, body string
		want               int
	}{
		{"PATCH", fmt.Sprintf("/api/admin/v1/serveraccess/tenants/%d", f.tenant), `{"status":"suspended","serverLimit":3}`, 200},
		{"PATCH", fmt.Sprintf("/api/admin/v1/serveraccess/servers/%d", f.server), `{"status":"suspended","verifyRateLimit":55}`, 200},
		{"PUT", "/api/admin/v1/serveraccess/fallback", `{"enabled":false}`, 200},
		{"GET", "/api/admin/v1/serveraccess/audit", "", 200},
	} {
		rec := f.request(t, f.super, tc.method, tc.path, tc.body, "")
		if rec.Code != tc.want {
			t.Errorf("%s %s = %d %s, want %d", tc.method, tc.path, rec.Code, rec.Body.String(), tc.want)
		}
	}
	tenant, err := f.st.SATenantByID(context.Background(), f.tenant)
	if err != nil || tenant.Status != "suspended" || tenant.ServerLimit != 3 {
		t.Fatalf("tenant update = %+v %v", tenant, err)
	}
	policy, err := f.st.SAServerPolicy(context.Background(), f.tenant, f.server)
	if err != nil || policy.VerifyRateLimit != 55 {
		t.Fatalf("quota update = %+v %v", policy, err)
	}
	if raw, ok, err := f.st.GetOption(context.Background(), "serveraccess_global_fallback"); err != nil || !ok || raw != "0" {
		t.Fatalf("fallback = %q ok=%v err=%v", raw, ok, err)
	}
	if _, _, auditErr := f.st.PlatformServerAccessAudit(context.Background(), "", 1, 20); auditErr != nil {
		t.Logf("direct audit error: %v", auditErr)
	}
	rec := f.request(t, f.super, "GET", "/api/admin/v1/serveraccess/audit", "", "")
	if !strings.Contains(rec.Body.String(), "serveraccess_tenant_status_update") || !strings.Contains(rec.Body.String(), "serveraccess_fallback_update") {
		t.Fatalf("audit missing actions: %s", rec.Body.String())
	}
}

func TestServerAccessAPIPlatformServersExposeCurrentPolicyAndCredentialSummaries(t *testing.T) {
	f := newSAFixture(t)
	whitelist, fallback := true, false
	if err := f.st.UpdateSAServerOwnerPolicy(context.Background(), f.tenant, f.server, &whitelist, &fallback); err != nil {
		t.Fatal(err)
	}
	rate, burst, concurrent, whitelistLimit, retention := 41, 7, 3, 19, 14
	if err := f.st.UpdateSAServerPlatformPolicy(context.Background(), f.tenant, f.server, &rate, &burst, &concurrent, &whitelistLimit, &retention); err != nil {
		t.Fatal(err)
	}
	activeHash, revokedHash := strings.Repeat("e", 64), strings.Repeat("f", 64)
	activeID, err := f.st.CreateSAServerCredential(context.Background(), f.tenant, f.server, store.SACredential{
		Mode: "header", SecretHash: activeHash, Scopes: `["verify:join"]`, Status: "active",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.st.TouchSACredential(context.Background(), f.tenant, f.server, activeID, f.now); err != nil {
		t.Fatal(err)
	}
	expires := f.now.Add(48 * time.Hour)
	revokedID, err := f.st.CreateSAServerCredential(context.Background(), f.tenant, f.server, store.SACredential{
		Mode: "url_secret", SecretHash: revokedHash, Scopes: `["verify:join"]`, Status: "revoked",
		ExpiresAt: sql.NullTime{Time: expires, Valid: true},
	})
	if err != nil {
		t.Fatal(err)
	}

	rec := f.request(t, f.super, "GET", "/api/admin/v1/serveraccess/servers?page=1&size=20", "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("platform servers = %d %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), activeHash) || strings.Contains(rec.Body.String(), revokedHash) ||
		strings.Contains(rec.Body.String(), "secretHash") || strings.Contains(rec.Body.String(), `"secret"`) {
		t.Fatalf("platform server list leaked credential material: %s", rec.Body.String())
	}
	var response struct {
		Servers []struct {
			ID          int64
			Policy      map[string]any
			Credentials []map[string]any
		}
		Total int
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Total != 2 || len(response.Servers) != 2 {
		t.Fatalf("platform server page = %+v", response)
	}
	var got *struct {
		ID          int64
		Policy      map[string]any
		Credentials []map[string]any
	}
	for i := range response.Servers {
		if response.Servers[i].ID == f.server {
			got = &response.Servers[i]
		}
	}
	if got == nil {
		t.Fatalf("platform server list omitted server %d: %s", f.server, rec.Body.String())
	}
	wantPolicy := map[string]any{
		"whitelistEnabled": true, "premiumFallbackEnabled": false,
		"verifyRateLimit": float64(41), "verifyBurst": float64(7), "maxConcurrent": float64(3),
		"whitelistLimit": float64(19), "eventRetentionDays": float64(14),
	}
	if !reflect.DeepEqual(got.Policy, wantPolicy) {
		t.Fatalf("platform policy = %#v, want %#v", got.Policy, wantPolicy)
	}
	wantCredentials := []map[string]any{
		{"id": float64(revokedID), "mode": "url_secret", "status": "revoked", "lastUsedAt": nil, "expiresAt": expires.Format(time.RFC3339)},
		{"id": float64(activeID), "mode": "header", "status": "active", "lastUsedAt": f.now.Format(time.RFC3339), "expiresAt": nil},
	}
	if !reflect.DeepEqual(got.Credentials, wantCredentials) {
		t.Fatalf("platform credentials = %#v, want %#v", got.Credentials, wantCredentials)
	}
	for _, row := range response.Servers {
		if row.ID == f.otherServer && row.Credentials == nil {
			t.Fatal("server without credentials must return an empty summary list")
		}
	}
}

func TestServerAccessAPIOwnerCannotUndoPlatformSuspension(t *testing.T) {
	f := newSAFixture(t)
	if err := f.st.UpdateSAServer(context.Background(), f.tenant, f.server, "one", "suspended"); err != nil {
		t.Fatal(err)
	}
	rec := f.request(t, f.owner, "PATCH", fmt.Sprintf("/api/serveraccess/v1/servers/%d", f.server), `{"status":"active"}`, "")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("owner reactivate suspended = %d %s, want 403", rec.Code, rec.Body.String())
	}
	server, err := f.st.SAServerByID(context.Background(), f.tenant, f.server)
	if err != nil || server.Status != "suspended" {
		t.Fatalf("platform suspension was undone: %+v %v", server, err)
	}
}

func TestServerAccessAPIRejectsMalformedAndViewerMutations(t *testing.T) {
	f := newSAFixture(t)
	base := fmt.Sprintf("/api/serveraccess/v1/servers/%d", f.server)
	for _, tc := range []struct {
		uid                int64
		method, path, body string
		want               int
	}{
		{f.viewer, "PATCH", base, `{"name":"x"}`, 403},
		{f.viewer, "POST", base + "/whitelist", `{"uid":1}`, 403},
		{f.viewer, "POST", base + "/bans", `{"uid":1}`, 403},
		{f.viewer, "POST", base + "/credentials", `{"mode":"header"}`, 403},
		{f.owner, "POST", "/api/serveraccess/v1/servers", `{"name":`, 400},
		{f.owner, "POST", "/api/serveraccess/v1/servers", `{"name":"x"} {}`, 400},
		{f.owner, "POST", "/api/serveraccess/v1/servers", `{"name":"x","status":"suspended"}`, 400},
		{f.owner, "PATCH", base, `{}`, 400},
		{f.owner, "POST", base + "/credentials", `{"mode":"wrong"}`, 400},
		{f.owner, "POST", base + "/bans", `{"uid":1,"expiresAt":"2020-01-01T00:00:00Z"}`, 400},
		{f.owner, "POST", "/api/serveraccess/v1/invites/accept", `{}`, 400},
		{f.owner, "POST", "/api/serveraccess/v1/invites/accept", `{"token":`, 400},
		{f.owner, "POST", "/api/serveraccess/v1/invites/accept", `{"token":"x","tenant_id":1}`, 400},
		{f.owner, "POST", "/api/serveraccess/v1/servers", `{"name":"` + strings.Repeat("界", 191) + `"}`, 201},
	} {
		rec := f.request(t, tc.uid, tc.method, tc.path, tc.body, "")
		if rec.Code != tc.want {
			t.Errorf("%s %s = %d %s, want %d", tc.method, tc.path, rec.Code, rec.Body.String(), tc.want)
		}
	}
}

func TestServerAccessAPIPlatformCanRevokeCredential(t *testing.T) {
	f := newSAFixture(t)
	cid, err := f.st.CreateSAServerCredential(context.Background(), f.tenant, f.server, store.SACredential{Mode: "header", SecretHash: strings.Repeat("d", 64), Scopes: `["verify:join"]`, Status: "active"})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/admin/v1/serveraccess/servers/%d/credentials/%d/revoke", f.server, cid)
	if rec := f.request(t, f.owner, "POST", path, `{}`, ""); rec.Code != http.StatusForbidden {
		t.Fatalf("owner platform revoke = %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.request(t, f.super, "POST", path, `{}`, ""); rec.Code != http.StatusOK {
		t.Fatalf("platform revoke = %d %s", rec.Code, rec.Body.String())
	}
	if _, err := f.st.SACredentialBySecretHash(context.Background(), strings.Repeat("d", 64), f.now); err == nil {
		t.Fatal("platform-revoked credential still resolves")
	}
}

func TestServerAccessAPIDisabledGateDoesNotTouchStore(t *testing.T) {
	f := newSAFixture(t)
	f.stack.SetEnabled(false)
	rec := f.request(t, f.owner, "POST", "/api/serveraccess/v1/servers", `{"name":"disabled"}`, "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("disabled = %d %s", rec.Code, rec.Body.String())
	}
	rows, err := f.st.ListSAServers(context.Background(), f.tenant, "")
	if err != nil || len(rows) != 1 {
		t.Fatalf("disabled write changed rows: %+v %v", rows, err)
	}
}

func TestServerAccessAPIResponseIsJSON(t *testing.T) {
	f := newSAFixture(t)
	rec := f.request(t, f.owner, "GET", "/api/serveraccess/v1/tenants/me", "", "")
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "application/json" || !bytes.Contains(rec.Body.Bytes(), []byte(`"tenantId"`)) {
		t.Fatalf("tenant response = %d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
}

func TestServerAccessAPIOwnerEventsAreScopedMinimalAndCursorPaged(t *testing.T) {
	f := newSAFixture(t)
	from, to := f.now.Add(-time.Minute), f.now.Add(time.Minute)
	for i, outcome := range []string{"allow", "deny", "allow"} {
		event := store.SAJoinEvent{
			TenantID: f.tenant, ServerID: f.server, UID: sql.NullInt64{Int64: f.outsider, Valid: true},
			UUID: "0123456789abcdef0123456789abcdef", PlayerName: "PrivatePlayer",
			Outcome: outcome, ReasonCode: "fixture_reason", SourceMode: "url_secret", DurationMS: int64(i + 1),
			RequestID: fmt.Sprintf("event-request-%d", i),
		}
		if err := f.st.InsertSAJoinEvent(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.st.InsertSAJoinEvent(context.Background(), store.SAJoinEvent{
		TenantID: f.otherTenant, ServerID: f.otherServer, Outcome: "allow", ReasonCode: "other_tenant",
		SourceMode: "header", RequestID: "other-tenant-request",
	}); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/serveraccess/v1/servers/%d/events?size=2&from=%s&to=%s", f.server,
		from.Format(time.RFC3339), to.Format(time.RFC3339))
	rec := f.request(t, f.viewer, "GET", path, "", "")
	if rec.Code != http.StatusOK {
		t.Fatalf("viewer events = %d %s, want 200", rec.Code, rec.Body.String())
	}
	var first struct {
		Events     []map[string]any `json:"events"`
		NextBefore int64            `json:"nextBefore"`
		HasMore    bool             `json:"hasMore"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &first); err != nil {
		t.Fatal(err)
	}
	if len(first.Events) != 2 || !first.HasMore || first.NextBefore <= 0 {
		t.Fatalf("first event page = %s", rec.Body.String())
	}
	if first.Events[0]["id"] == first.Events[1]["id"] {
		t.Fatalf("event cursor did not advance: %s", rec.Body.String())
	}
	// The cursor is an id, so pages must follow descending insertion order
	// rather than a timestamp order that can skip concurrently committed rows.
	if first.Events[0]["durationMs"] != float64(3) || first.Events[1]["durationMs"] != float64(2) {
		t.Fatalf("event page is not in descending id order: %s", rec.Body.String())
	}
	for _, event := range first.Events {
		if len(event) != 6 {
			t.Fatalf("owner event exposed fields beyond the timeline contract: %#v", event)
		}
		for _, key := range []string{"id", "createdAt", "outcome", "reasonCode", "durationMs", "sourceMode"} {
			if _, ok := event[key]; !ok {
				t.Fatalf("owner event missing %s: %#v", key, event)
			}
		}
	}
	for _, forbidden := range []string{"PrivatePlayer", "outsider@example.com", "uid", "uuid", "requestId", "joinSessionId", "tenantId", "serverId"} {
		if strings.Contains(rec.Body.String(), forbidden) {
			t.Fatalf("owner events leaked %q: %s", forbidden, rec.Body.String())
		}
	}
	second := f.request(t, f.owner, "GET", fmt.Sprintf("%s&before=%d", path, first.NextBefore), "", "")
	var next struct {
		Events     []map[string]any `json:"events"`
		NextBefore int64            `json:"nextBefore"`
		HasMore    bool             `json:"hasMore"`
	}
	if err := json.Unmarshal(second.Body.Bytes(), &next); err != nil {
		t.Fatal(err)
	}
	if second.Code != http.StatusOK || len(next.Events) != 1 || next.HasMore || next.NextBefore != 0 {
		t.Fatalf("second event page = %d %s", second.Code, second.Body.String())
	}
	for _, old := range first.Events {
		if old["id"] == next.Events[0]["id"] {
			t.Fatalf("cursor page repeated an event: %s", second.Body.String())
		}
	}
	for _, tc := range []struct {
		uid  int64
		path string
		want int
	}{
		{0, path, 401},
		{f.legacyAdmin, path, 403},
		{f.owner, fmt.Sprintf("/api/serveraccess/v1/servers/%d/events", f.otherServer), 404},
		{f.owner, fmt.Sprintf("/api/serveraccess/v1/servers/%d/events?size=201", f.server), 200},
		{f.owner, fmt.Sprintf("/api/serveraccess/v1/servers/%d/events?before=-1", f.server), 400},
		{f.owner, fmt.Sprintf("/api/serveraccess/v1/servers/%d/events?from=not-a-time", f.server), 400},
	} {
		if got := f.request(t, tc.uid, "GET", tc.path, "", ""); got.Code != tc.want {
			t.Errorf("GET %s as %d = %d %s, want %d", tc.path, tc.uid, got.Code, got.Body.String(), tc.want)
		}
	}
}

func TestServerAccessAPIPlayerLookupUsesOwnerSessionAndReturnsOnlyIdentity(t *testing.T) {
	f := newSAFixture(t)
	_, uuid, err := f.st.CreatePlayer(context.Background(), f.outsider, "LookupPlayer")
	if err != nil {
		t.Fatal(err)
	}
	want := fmt.Sprintf(`{"uid":%d,"name":"LookupPlayer","uuid":%q}`, f.outsider, uuid)
	for _, q := range []string{"LookupPlayer", "lookupplayer", uuid} {
		rec := f.request(t, f.viewer, "GET", "/api/serveraccess/v1/players/lookup?q="+q, "", "")
		if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != want {
			t.Fatalf("lookup %q = %d %s, want 200 %s", q, rec.Code, rec.Body.String(), want)
		}
	}
	for _, tc := range []struct {
		uid  int64
		path string
		want int
	}{
		{0, "/api/serveraccess/v1/players/lookup?q=LookupPlayer", 401},
		{f.legacyAdmin, "/api/serveraccess/v1/players/lookup?q=LookupPlayer", 403},
		{f.owner, "/api/serveraccess/v1/players/lookup?q=MissingPlayer", 404},
		{f.owner, "/api/serveraccess/v1/players/lookup", 400},
		{f.owner, "/api/serveraccess/v1/players/lookup?q=" + strings.Repeat("x", 65), 400},
	} {
		if rec := f.request(t, tc.uid, "GET", tc.path, "", ""); rec.Code != tc.want {
			t.Errorf("GET %s as %d = %d %s, want %d", tc.path, tc.uid, rec.Code, rec.Body.String(), tc.want)
		}
	}
}

func TestServerAccessAPIS4ReadsRespectDisabledGate(t *testing.T) {
	f := newSAFixture(t)
	f.stack.SetEnabled(false)
	for _, path := range []string{
		fmt.Sprintf("/api/serveraccess/v1/servers/%d/events", f.server),
		"/api/serveraccess/v1/players/lookup?q=LookupPlayer",
	} {
		if rec := f.request(t, f.owner, "GET", path, "", ""); rec.Code != http.StatusNotFound {
			t.Fatalf("disabled GET %s = %d %s, want 404", path, rec.Code, rec.Body.String())
		}
	}
}
