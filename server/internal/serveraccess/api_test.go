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
	f := &saFixture{st: st, now: now}
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
	s := NewStack(Config{Enabled: true, Store: st, Now: func() time.Time { return now }})
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
	invite := f.request(t, f.owner, "POST", "/api/serveraccess/v1/tenants/me/members/invite", `{"email":"outsider@example.com","role":"viewer"}`, "")
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
		return f.request(t, f.outsider, "POST", "/api/serveraccess/v1/invites/accept", string(b), "")
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
