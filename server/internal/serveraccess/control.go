package serveraccess

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"skin-nexus/server/internal/adminapi"
	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/store"
)

const sessionCookieName = "skin_session"
const maxControlBody = 64 << 10

type control struct {
	cfg Config
}

type caller struct {
	UID      int64
	TenantID int64
	Role     string
}

type callerKey struct{}

func (s *Stack) controlHandler() http.Handler {
	c := &control{cfg: s.cfg}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/serveraccess/v1/health", func(w http.ResponseWriter, _ *http.Request) {
		writeSAJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.Handle("GET /api/serveraccess/v1/tenants/me", c.owner(http.HandlerFunc(c.tenantMe)))
	mux.Handle("GET /api/serveraccess/v1/tenants/me/export", c.ownerLifecycle(http.HandlerFunc(c.tenantExport), "admin", "owner"))
	mux.Handle("POST /api/serveraccess/v1/tenants/me/delete-request", c.owner(http.HandlerFunc(c.tenantDeleteRequest), "owner"))
	mux.Handle("GET /api/serveraccess/v1/tenants/me/members", c.owner(http.HandlerFunc(c.members)))
	mux.Handle("POST /api/serveraccess/v1/tenants/me/members/invite", c.owner(http.HandlerFunc(c.invite), "owner"))
	mux.Handle("DELETE /api/serveraccess/v1/tenants/me/members/{uid}", c.owner(http.HandlerFunc(c.removeMember), "owner"))
	mux.Handle("GET /api/serveraccess/v1/tenants/me/invites", c.owner(http.HandlerFunc(c.invites), "owner"))
	mux.Handle("DELETE /api/serveraccess/v1/tenants/me/invites/{id}", c.owner(http.HandlerFunc(c.revokeInvite), "owner"))
	mux.Handle("POST /api/serveraccess/v1/invites/accept", c.session(http.HandlerFunc(c.acceptInvite)))
	mux.Handle("GET /api/serveraccess/v1/servers", c.owner(http.HandlerFunc(c.servers)))
	mux.Handle("POST /api/serveraccess/v1/servers", c.owner(http.HandlerFunc(c.createServer), "admin", "owner"))
	mux.Handle("GET /api/serveraccess/v1/servers/{id}", c.owner(http.HandlerFunc(c.server)))
	mux.Handle("PATCH /api/serveraccess/v1/servers/{id}", c.owner(http.HandlerFunc(c.updateServer), "admin", "owner"))
	mux.Handle("GET /api/serveraccess/v1/servers/{id}/whitelist", c.owner(http.HandlerFunc(c.whitelist)))
	mux.Handle("POST /api/serveraccess/v1/servers/{id}/whitelist", c.owner(http.HandlerFunc(c.addWhitelist), "admin", "owner"))
	mux.Handle("DELETE /api/serveraccess/v1/servers/{id}/whitelist/{uid}", c.owner(http.HandlerFunc(c.removeWhitelist), "admin", "owner"))
	mux.Handle("GET /api/serveraccess/v1/servers/{id}/bans", c.owner(http.HandlerFunc(c.bans)))
	mux.Handle("POST /api/serveraccess/v1/servers/{id}/bans", c.owner(http.HandlerFunc(c.addBan), "admin", "owner"))
	mux.Handle("DELETE /api/serveraccess/v1/servers/{id}/bans/{uid}", c.owner(http.HandlerFunc(c.removeBan), "admin", "owner"))
	mux.Handle("GET /api/serveraccess/v1/servers/{id}/credentials", c.owner(http.HandlerFunc(c.credentials)))
	mux.Handle("POST /api/serveraccess/v1/servers/{id}/credentials", c.owner(http.HandlerFunc(c.createCredential), "admin", "owner"))
	mux.Handle("POST /api/serveraccess/v1/servers/{id}/credentials/{cid}/rotate", c.owner(http.HandlerFunc(c.rotateCredential), "admin", "owner"))
	mux.Handle("POST /api/serveraccess/v1/servers/{id}/credentials/{cid}/revoke", c.owner(http.HandlerFunc(c.revokeCredential), "admin", "owner"))
	mux.Handle("GET /api/serveraccess/v1/servers/{id}/export", c.owner(http.HandlerFunc(c.exportStub)))
	mux.Handle("GET /api/serveraccess/v1/servers/{id}/events", c.owner(http.HandlerFunc(c.events)))
	mux.Handle("GET /api/serveraccess/v1/players/lookup", c.owner(http.HandlerFunc(c.playerLookup)))

	mux.Handle("GET /api/admin/v1/serveraccess/tenants", c.platform(http.HandlerFunc(c.platformTenants)))
	mux.Handle("POST /api/admin/v1/serveraccess/tenants", c.platform(http.HandlerFunc(c.platformTenantCreate)))
	mux.Handle("GET /api/admin/v1/serveraccess/servers", c.platform(http.HandlerFunc(c.platformServers)))
	mux.Handle("PATCH /api/admin/v1/serveraccess/tenants/{id}", c.platform(http.HandlerFunc(c.platformTenantUpdate)))
	mux.Handle("POST /api/admin/v1/serveraccess/tenants/{id}/delete", c.platform(http.HandlerFunc(c.platformTenantDelete)))
	mux.Handle("PATCH /api/admin/v1/serveraccess/servers/{id}", c.platform(http.HandlerFunc(c.platformServerUpdate)))
	mux.Handle("POST /api/admin/v1/serveraccess/servers/{id}/credentials/{cid}/revoke", c.platform(http.HandlerFunc(c.platformRevoke)))
	mux.Handle("GET /api/admin/v1/serveraccess/fallback", c.platform(http.HandlerFunc(c.platformFallback)))
	mux.Handle("PUT /api/admin/v1/serveraccess/fallback", c.platform(http.HandlerFunc(c.platformFallback)))
	mux.Handle("GET /api/admin/v1/serveraccess/audit", c.platform(http.HandlerFunc(c.platformAudit)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions && !adminapi.SameOriginForAdmin(r) {
			writeSAError(w, http.StatusForbidden, i18n.MsgOriginMismatch, "Origin check failed")
			return
		}
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxControlBody)
		}
		mux.ServeHTTP(w, r)
	})
}

func (c *control) now() time.Time {
	if c.cfg.Now != nil {
		return c.cfg.Now().UTC()
	}
	return time.Now().UTC()
}

func (c *control) session(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, ok := c.webSession(r)
		if !ok {
			writeSAError(w, http.StatusUnauthorized, i18n.MsgUnauthorized, "A web session is required")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, caller{UID: uid})))
	})
}

func (c *control) owner(next http.HandlerFunc, roles ...string) http.Handler {
	return c.ownerMembership(next, false, roles...)
}

func (c *control) ownerLifecycle(next http.HandlerFunc, roles ...string) http.Handler {
	return c.ownerMembership(next, true, roles...)
}

func (c *control) ownerMembership(next http.HandlerFunc, includeDeleting bool, roles ...string) http.Handler {
	return c.session(func(w http.ResponseWriter, r *http.Request) {
		who := r.Context().Value(callerKey{}).(caller)
		member, ok, err := c.cfg.Store.SATenantMemberByUID(r.Context(), who.UID)
		if includeDeleting {
			member, ok, err = c.cfg.Store.SATenantLifecycleMemberByUID(r.Context(), who.UID)
		}
		if err != nil {
			writeSAError(w, http.StatusInternalServerError, i18n.MsgInternal, "Could not resolve tenant membership")
			return
		}
		if !ok {
			writeSAError(w, http.StatusForbidden, i18n.MsgServerAccessForbidden, "Tenant membership is required")
			return
		}
		if len(roles) > 0 && !saRoleAllowed(member.Role, roles) {
			writeSAError(w, http.StatusForbidden, i18n.MsgServerAccessForbidden, "Your tenant role cannot perform this action")
			return
		}
		who.TenantID, who.Role = member.TenantID, member.Role
		next(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, who)))
	})
}

func (c *control) platform(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Deliberately no X-Admin-Token branch: this surface requires a real
		// permission=2 web session so every emergency action has an actor.
		uid, ok := c.webSession(r)
		if !ok {
			writeSAError(w, http.StatusForbidden, i18n.MsgWebSessionRequired, "A super-admin web session is required")
			return
		}
		perm, err := c.cfg.Store.UserPermission(r.Context(), uid)
		if err != nil || perm != 2 {
			writeSAError(w, http.StatusForbidden, i18n.MsgSuperAdminRequired, "Super-admin permission is required")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), callerKey{}, caller{UID: uid})))
	})
}

func (c *control) webSession(r *http.Request) (int64, bool) {
	if c.cfg.Store == nil {
		return 0, false
	}
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil || cookie.Value == "" {
		return 0, false
	}
	uid, err := c.cfg.Store.SessionUser(r.Context(), cookie.Value)
	if err != nil || uid <= 0 {
		return 0, false
	}
	perm, err := c.cfg.Store.UserPermission(r.Context(), uid)
	return uid, err == nil && perm >= 0
}

func saRoleAllowed(role string, allowed []string) bool {
	if role == "owner" {
		return true
	}
	for _, candidate := range allowed {
		if role == candidate {
			return true
		}
	}
	return false
}

func saCaller(r *http.Request) caller { return r.Context().Value(callerKey{}).(caller) }

func saID(r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	return id, err == nil && id > 0
}

func decodeSAJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	mediaType, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mediaType != "application/json" && r.Header.Get("X-Requested-With") != "XMLHttpRequest" {
		writeSAError(w, http.StatusUnsupportedMediaType, i18n.MsgBadBody, "Request body must use application/json")
		return false
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeSAError(w, http.StatusBadRequest, i18n.MsgBadBody, "Request body must be a valid JSON object")
		return false
	}
	var extra any
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		writeSAError(w, http.StatusBadRequest, i18n.MsgBadBody, "Request body must contain exactly one JSON object")
		return false
	}
	return true
}

func writeSAJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeSAError(w http.ResponseWriter, status int, code, message string) {
	writeSAJSON(w, status, map[string]string{"error": http.StatusText(status), "code": code, "message": message})
}

func writeSAStoreError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeSAError(w, http.StatusNotFound, i18n.MsgServerAccessNotFound, "Resource not found")
	case errors.Is(err, store.ErrForbidden):
		writeSAError(w, http.StatusForbidden, i18n.MsgServerAccessForbidden, "Action not allowed")
	case errors.Is(err, store.ErrDuplicate):
		writeSAError(w, http.StatusConflict, i18n.MsgServerAccessConflict, "Resource already exists")
	case errors.Is(err, store.ErrDeletionInProgress):
		writeSAError(w, http.StatusConflict, i18n.MsgServerAccessConflict, "Tenant deletion is already in progress")
	case errors.Is(err, store.ErrQuota):
		writeSAError(w, http.StatusConflict, i18n.MsgServerAccessQuota, "Server quota exceeded")
	case errors.Is(err, store.ErrLastOwner):
		writeSAError(w, http.StatusConflict, i18n.MsgServerAccessLastOwner, "A tenant must retain at least one owner")
	default:
		writeSAError(w, http.StatusInternalServerError, i18n.MsgInternal, "Could not complete the request")
	}
}

func saText(value string, max int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) > max {
		runes = runes[:max]
	}
	return string(runes)
}
