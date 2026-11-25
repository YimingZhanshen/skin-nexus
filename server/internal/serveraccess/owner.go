package serveraccess

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/store"
)

func (c *control) tenantMe(w http.ResponseWriter, r *http.Request) {
	who := saCaller(r)
	tenant, err := c.cfg.Store.SATenantByID(r.Context(), who.TenantID)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	writeSAJSON(w, http.StatusOK, map[string]any{"tenantId": tenant.ID, "tenant": tenant, "role": who.Role})
}

func (c *control) members(w http.ResponseWriter, r *http.Request) {
	rows, err := c.cfg.Store.ListSATenantMembers(r.Context(), saCaller(r).TenantID)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	writeSAJSON(w, http.StatusOK, map[string]any{"members": rows})
}

func (c *control) invite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if !decodeSAJSON(w, r, &body) {
		return
	}
	body.Email = strings.ToLower(strings.TrimSpace(body.Email))
	if !validEmail(body.Email) || (body.Role != "admin" && body.Role != "viewer") {
		writeSAError(w, http.StatusBadRequest, i18n.MsgServerAccessInvalid, "A valid email and admin or viewer role are required")
		return
	}
	secret, err := saRandomToken()
	if err != nil {
		writeSAError(w, http.StatusInternalServerError, i18n.MsgInternal, "Could not create invitation")
		return
	}
	ttl := c.cfg.InviteTTL
	if ttl <= 0 {
		ttl = 72 * time.Hour
	}
	who := saCaller(r)
	id, err := c.cfg.Store.CreateSATenantInvite(r.Context(), who.TenantID, body.Email, body.Role, saHash(secret), c.now().Add(ttl))
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	c.cfg.Store.InsertAudit(r.Context(), who.UID, "serveraccess_invite_create",
		map[string]any{"tenantId": who.TenantID, "inviteId": id, "role": body.Role})
	writeSAJSON(w, http.StatusCreated, map[string]any{"id": id, "token": secret, "expiresAt": c.now().Add(ttl)})
}

func (c *control) invites(w http.ResponseWriter, r *http.Request) {
	page, size := saOwnerPage(r)
	rows, total, err := c.cfg.Store.ListSATenantInvitesPage(r.Context(), saCaller(r).TenantID, page, size)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	writeSAJSON(w, http.StatusOK, map[string]any{"invites": rows, "total": total, "page": page, "size": size})
}

func (c *control) revokeInvite(w http.ResponseWriter, r *http.Request) {
	id, ok := saID(r, "id")
	if !ok {
		writeSAError(w, http.StatusBadRequest, i18n.MsgBadID, "Invalid invitation id")
		return
	}
	who := saCaller(r)
	if err := c.cfg.Store.RevokeSATenantInvite(r.Context(), who.TenantID, id); err != nil {
		writeSAStoreError(w, err)
		return
	}
	c.cfg.Store.InsertAudit(r.Context(), who.UID, "serveraccess_invite_revoke",
		map[string]any{"tenantId": who.TenantID, "inviteId": id})
	writeSAJSON(w, http.StatusOK, map[string]any{"revoked": true})
}

func (c *control) acceptInvite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Token string `json:"token"`
	}
	if !decodeSAJSON(w, r, &body) {
		return
	}
	if strings.TrimSpace(body.Token) == "" {
		writeSAError(w, http.StatusBadRequest, i18n.MsgServerAccessInvite, "Invitation token is required")
		return
	}
	who := saCaller(r)
	email, verified, err := c.cfg.Store.SAUserEmail(r.Context(), who.UID)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	if !verified {
		writeSAError(w, http.StatusForbidden, i18n.MsgEmailVerifyRequired, "Verify your email address before accepting an invitation")
		return
	}
	tenantID, err := c.cfg.Store.AcceptSATenantInvite(r.Context(), saHash(body.Token), who.UID, email, c.now())
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeSAError(w, http.StatusNotFound, i18n.MsgServerAccessInvite, "Invitation is invalid, expired, or already used")
		} else {
			writeSAStoreError(w, err)
		}
		return
	}
	c.cfg.Store.InsertAudit(r.Context(), who.UID, "serveraccess_invite_accept",
		map[string]any{"tenantId": tenantID, "uid": who.UID})
	writeSAJSON(w, http.StatusOK, map[string]any{"tenantId": tenantID})
}

func (c *control) removeMember(w http.ResponseWriter, r *http.Request) {
	uid, ok := saID(r, "uid")
	if !ok {
		writeSAError(w, http.StatusBadRequest, i18n.MsgBadID, "Invalid member id")
		return
	}
	who := saCaller(r)
	if uid == who.UID {
		writeSAError(w, http.StatusConflict, i18n.MsgServerAccessLastOwner, "You cannot remove your own membership")
		return
	}
	if err := c.cfg.Store.RemoveSATenantMember(r.Context(), who.TenantID, uid); err != nil {
		writeSAStoreError(w, err)
		return
	}
	c.cfg.Store.InsertAudit(r.Context(), who.UID, "serveraccess_member_remove",
		map[string]any{"tenantId": who.TenantID, "uid": uid})
	writeSAJSON(w, http.StatusOK, map[string]any{"removed": true})
}

func (c *control) servers(w http.ResponseWriter, r *http.Request) {
	page, size := saOwnerPage(r)
	tenantID := saCaller(r).TenantID
	rows, total, err := c.cfg.Store.ListSAServersPage(r.Context(), tenantID, r.URL.Query().Get("status"), page, size)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	quotaUsed, err := c.cfg.Store.CountSAServers(r.Context(), tenantID)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	writeSAJSON(w, http.StatusOK, map[string]any{
		"servers": rows, "total": total, "quotaUsed": quotaUsed, "page": page, "size": size,
	})
}

func (c *control) createServer(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if !decodeSAJSON(w, r, &body) {
		return
	}
	body.Name = saText(body.Name, 191)
	if body.Name == "" {
		writeSAError(w, http.StatusBadRequest, i18n.MsgMissingField, "Server name is required")
		return
	}
	publicID, err := saRandomToken()
	if err != nil {
		writeSAError(w, http.StatusInternalServerError, i18n.MsgInternal, "Could not create server")
		return
	}
	who := saCaller(r)
	server, err := c.cfg.Store.CreateSAServer(r.Context(), who.TenantID, "srv_"+publicID, body.Name)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	c.cfg.Store.InsertAudit(r.Context(), who.UID, "serveraccess_server_create",
		map[string]any{"tenantId": who.TenantID, "serverId": server.ID})
	writeSAJSON(w, http.StatusCreated, server)
}

func (c *control) server(w http.ResponseWriter, r *http.Request) {
	id, ok := saID(r, "id")
	if !ok {
		writeSAError(w, http.StatusBadRequest, i18n.MsgBadID, "Invalid server id")
		return
	}
	who := saCaller(r)
	server, err := c.cfg.Store.SAServerByID(r.Context(), who.TenantID, id)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	policy, err := c.cfg.Store.SAServerPolicy(r.Context(), who.TenantID, id)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	writeSAJSON(w, http.StatusOK, map[string]any{"server": server, "policy": policy})
}

func (c *control) updateServer(w http.ResponseWriter, r *http.Request) {
	id, ok := saID(r, "id")
	if !ok {
		writeSAError(w, http.StatusBadRequest, i18n.MsgBadID, "Invalid server id")
		return
	}
	var body struct {
		Name   *string `json:"name"`
		Status *string `json:"status"`
		Policy *struct {
			WhitelistEnabled       *bool `json:"whitelistEnabled"`
			PremiumFallbackEnabled *bool `json:"premiumFallbackEnabled"`
		} `json:"policy"`
	}
	if !decodeSAJSON(w, r, &body) {
		return
	}
	if body.Name == nil && body.Status == nil && body.Policy == nil {
		writeSAError(w, http.StatusBadRequest, i18n.MsgNoFields, "No fields to update")
		return
	}
	who := saCaller(r)
	current, err := c.cfg.Store.SAServerByID(r.Context(), who.TenantID, id)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	patch := store.SAOwnerServerPatch{Name: body.Name, Status: body.Status}
	if body.Name != nil {
		name := saText(*body.Name, 191)
		if name == "" {
			writeSAError(w, http.StatusBadRequest, i18n.MsgServerAccessInvalid, "Server name cannot be empty")
			return
		}
		patch.Name = &name
	}
	if body.Status != nil {
		if current.Status == "suspended" {
			writeSAError(w, http.StatusForbidden, i18n.MsgServerAccessForbidden, "Only the platform can change a suspended server")
			return
		}
		if *body.Status != "active" && *body.Status != "disabled" {
			writeSAError(w, http.StatusBadRequest, i18n.MsgServerAccessInvalid, "Owner status must be active or disabled")
			return
		}
	}
	if body.Policy != nil {
		patch.WhitelistEnabled = body.Policy.WhitelistEnabled
		patch.PremiumFallbackEnabled = body.Policy.PremiumFallbackEnabled
	}
	updated, err := c.cfg.Store.UpdateSAServerOwnerAudited(r.Context(), who.TenantID, id, patch, who.UID)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	writeSAJSON(w, http.StatusOK, map[string]any{"id": updated.ID, "name": updated.Name, "status": updated.Status})
}

func (c *control) whitelist(w http.ResponseWriter, r *http.Request) {
	c.listServerRows(w, r, func(tenant, server int64, page, size int) (any, int, error) {
		return c.cfg.Store.ListSAServerWhitelistPage(r.Context(), tenant, server, page, size)
	}, "whitelist")
}

func (c *control) addWhitelist(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UID  int64  `json:"uid"`
		Note string `json:"note"`
	}
	if !decodeSAJSON(w, r, &body) || body.UID <= 0 {
		writeSAError(w, http.StatusBadRequest, i18n.MsgServerAccessInvalid, "A positive uid is required")
		return
	}
	id, ok := saID(r, "id")
	if !ok {
		writeSAError(w, http.StatusBadRequest, i18n.MsgBadID, "Invalid server id")
		return
	}
	who := saCaller(r)
	if err := c.cfg.Store.AddSAServerWhitelist(r.Context(), who.TenantID, id, body.UID, saText(body.Note, 255), who.UID); err != nil {
		writeSAStoreError(w, err)
		return
	}
	c.cfg.Store.InsertAudit(r.Context(), who.UID, "serveraccess_whitelist_add",
		map[string]any{"tenantId": who.TenantID, "serverId": id, "uid": body.UID})
	writeSAJSON(w, http.StatusCreated, map[string]any{"uid": body.UID})
}

func (c *control) removeWhitelist(w http.ResponseWriter, r *http.Request) {
	id, uid, ok := c.removeServerUID(w, r, c.cfg.Store.RemoveSAServerWhitelist)
	if !ok {
		return
	}
	who := saCaller(r)
	c.cfg.Store.InsertAudit(r.Context(), who.UID, "serveraccess_whitelist_remove",
		map[string]any{"tenantId": who.TenantID, "serverId": id, "uid": uid})
	writeSAJSON(w, http.StatusOK, map[string]any{"removed": true})
}

func (c *control) bans(w http.ResponseWriter, r *http.Request) {
	c.listServerRows(w, r, func(tenant, server int64, page, size int) (any, int, error) {
		return c.cfg.Store.ListSAServerBansPage(r.Context(), tenant, server, page, size)
	}, "bans")
}

func (c *control) addBan(w http.ResponseWriter, r *http.Request) {
	var body struct {
		UID       int64  `json:"uid"`
		Reason    string `json:"reason"`
		ExpiresAt string `json:"expiresAt"`
	}
	if !decodeSAJSON(w, r, &body) || body.UID <= 0 {
		writeSAError(w, http.StatusBadRequest, i18n.MsgServerAccessInvalid, "A positive uid is required")
		return
	}
	var expires sql.NullTime
	if body.ExpiresAt != "" {
		at, err := time.Parse(time.RFC3339, body.ExpiresAt)
		if err != nil || !at.After(c.now()) {
			writeSAError(w, http.StatusBadRequest, i18n.MsgServerAccessInvalid, "Ban expiry must be a future RFC3339 time")
			return
		}
		expires = sql.NullTime{Time: at.UTC(), Valid: true}
	}
	id, ok := saID(r, "id")
	if !ok {
		writeSAError(w, http.StatusBadRequest, i18n.MsgBadID, "Invalid server id")
		return
	}
	who := saCaller(r)
	if err := c.cfg.Store.AddSAServerBan(r.Context(), who.TenantID, id, body.UID, saText(body.Reason, 255), expires, who.UID); err != nil {
		writeSAStoreError(w, err)
		return
	}
	c.cfg.Store.InsertAudit(r.Context(), who.UID, "serveraccess_ban_add",
		map[string]any{"tenantId": who.TenantID, "serverId": id, "uid": body.UID})
	writeSAJSON(w, http.StatusCreated, map[string]any{"uid": body.UID})
}

func (c *control) removeBan(w http.ResponseWriter, r *http.Request) {
	id, uid, ok := c.removeServerUID(w, r, c.cfg.Store.RemoveSAServerBan)
	if !ok {
		return
	}
	who := saCaller(r)
	c.cfg.Store.InsertAudit(r.Context(), who.UID, "serveraccess_ban_remove",
		map[string]any{"tenantId": who.TenantID, "serverId": id, "uid": uid})
	writeSAJSON(w, http.StatusOK, map[string]any{"removed": true})
}

func (c *control) listServerRows(w http.ResponseWriter, r *http.Request, load func(int64, int64, int, int) (any, int, error), key string) {
	id, ok := saID(r, "id")
	if !ok {
		writeSAError(w, http.StatusBadRequest, i18n.MsgBadID, "Invalid server id")
		return
	}
	who := saCaller(r)
	if _, err := c.cfg.Store.SAServerByID(r.Context(), who.TenantID, id); err != nil {
		writeSAStoreError(w, err)
		return
	}
	page, size := saOwnerPage(r)
	rows, total, err := load(who.TenantID, id, page, size)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	writeSAJSON(w, http.StatusOK, map[string]any{key: rows, "total": total, "page": page, "size": size})
}

func (c *control) removeServerUID(w http.ResponseWriter, r *http.Request, remove func(context.Context, int64, int64, int64) error) (int64, int64, bool) {
	id, ok := saID(r, "id")
	uid, uidOK := saID(r, "uid")
	if !ok || !uidOK {
		writeSAError(w, http.StatusBadRequest, i18n.MsgBadID, "Invalid server or player id")
		return 0, 0, false
	}
	who := saCaller(r)
	if err := remove(r.Context(), who.TenantID, id, uid); err != nil {
		writeSAStoreError(w, err)
		return 0, 0, false
	}
	return id, uid, true
}

func (c *control) exportStub(w http.ResponseWriter, _ *http.Request) {
	writeSAError(w, http.StatusNotFound, i18n.MsgServerAccessNotImpl, "Export is not implemented")
}

func validEmail(email string) bool {
	return len(email) <= 191 && strings.Count(email, "@") == 1 && !strings.ContainsAny(email, " \t\r\n")
}

func saRandomToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func saHash(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func saCredentialJSON(w http.ResponseWriter, status int, credential store.SACredential, secret string) {
	writeSAJSON(w, status, map[string]any{"credential": saViewCredential(credential), "secret": secret})
}

func saScopeJSON(scopes string) []string {
	var values []string
	if json.Unmarshal([]byte(scopes), &values) != nil {
		return []string{}
	}
	return values
}
