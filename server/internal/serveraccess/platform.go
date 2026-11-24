package serveraccess

import (
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"skin-nexus/server/internal/i18n"
)

func (c *control) platformTenants(w http.ResponseWriter, r *http.Request) {
	page, size := saPage(r)
	rows, total, err := c.cfg.Store.PlatformSATenants(r.Context(), r.URL.Query().Get("q"), page, size)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	writeSAJSON(w, http.StatusOK, map[string]any{"tenants": rows, "total": total, "page": page, "size": size})
}

func (c *control) platformTenantCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name        string `json:"name"`
		ServerLimit int    `json:"serverLimit"`
		OwnerUID    int64  `json:"ownerUid"`
	}
	if !decodeSAJSON(w, r, &body) {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if body.Name == "" || utf8.RuneCountInString(body.Name) > 190 || body.ServerLimit < 1 || body.ServerLimit > 10000 || body.OwnerUID < 1 {
		writeSAError(w, http.StatusBadRequest, i18n.MsgServerAccessInvalid,
			"Name is required (at most 190 characters), server limit must be between 1 and 10000, and owner UID must be positive")
		return
	}
	tenant, owner, err := c.cfg.Store.CreateSATenantWithOwner(r.Context(), body.Name, body.ServerLimit, body.OwnerUID)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	c.cfg.Store.InsertAudit(r.Context(), saCaller(r).UID, "serveraccess_tenant_create",
		map[string]any{"tenantId": tenant.ID, "ownerUid": owner.UID, "serverLimit": tenant.ServerLimit})
	writeSAJSON(w, http.StatusCreated, map[string]any{"tenant": tenant, "owner": owner})
}

func (c *control) platformServers(w http.ResponseWriter, r *http.Request) {
	page, size := saPage(r)
	rows, total, err := c.cfg.Store.PlatformSAServers(r.Context(), r.URL.Query().Get("q"), page, size)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	writeSAJSON(w, http.StatusOK, map[string]any{"servers": rows, "total": total, "page": page, "size": size})
}

func (c *control) platformTenantUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := saID(r, "id")
	if !ok {
		writeSAError(w, http.StatusBadRequest, i18n.MsgBadID, "Invalid tenant id")
		return
	}
	var body struct {
		Status      string `json:"status"`
		ServerLimit *int   `json:"serverLimit"`
	}
	if !decodeSAJSON(w, r, &body) {
		return
	}
	if body.Status != "" && body.Status != "active" && body.Status != "suspended" {
		writeSAError(w, http.StatusBadRequest, i18n.MsgServerAccessInvalid, "Tenant status must be active or suspended")
		return
	}
	if body.ServerLimit != nil && (*body.ServerLimit < 1 || *body.ServerLimit > 10000) {
		writeSAError(w, http.StatusBadRequest, i18n.MsgServerAccessInvalid, "Server limit must be between 1 and 10000")
		return
	}
	if body.Status == "" && body.ServerLimit == nil {
		writeSAError(w, http.StatusBadRequest, i18n.MsgNoFields, "No fields to update")
		return
	}
	who := saCaller(r)
	if err := c.cfg.Store.UpdateSATenantPlatformAudited(r.Context(), id, body.Status, body.ServerLimit, who.UID); err != nil {
		writeSAStoreError(w, err)
		return
	}
	writeSAJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (c *control) platformServerUpdate(w http.ResponseWriter, r *http.Request) {
	id, ok := saID(r, "id")
	if !ok {
		writeSAError(w, http.StatusBadRequest, i18n.MsgBadID, "Invalid server id")
		return
	}
	var body struct {
		Status             string `json:"status"`
		VerifyRateLimit    *int   `json:"verifyRateLimit"`
		VerifyBurst        *int   `json:"verifyBurst"`
		MaxConcurrent      *int   `json:"maxConcurrent"`
		WhitelistLimit     *int   `json:"whitelistLimit"`
		EventRetentionDays *int   `json:"eventRetentionDays"`
	}
	if !decodeSAJSON(w, r, &body) {
		return
	}
	if body.Status != "" && body.Status != "active" && body.Status != "disabled" && body.Status != "suspended" {
		writeSAError(w, http.StatusBadRequest, i18n.MsgServerAccessInvalid, "Invalid server status")
		return
	}
	for _, value := range []*int{body.VerifyRateLimit, body.VerifyBurst, body.MaxConcurrent, body.WhitelistLimit, body.EventRetentionDays} {
		if value != nil && (*value < 1 || *value > 1000000) {
			writeSAError(w, http.StatusBadRequest, i18n.MsgServerAccessInvalid, "Quota values must be positive")
			return
		}
	}
	if body.Status == "" && body.VerifyRateLimit == nil && body.VerifyBurst == nil && body.MaxConcurrent == nil && body.WhitelistLimit == nil && body.EventRetentionDays == nil {
		writeSAError(w, http.StatusBadRequest, i18n.MsgNoFields, "No fields to update")
		return
	}
	server, err := c.cfg.Store.PlatformSAServerByID(r.Context(), id)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	who := saCaller(r)
	if err := c.cfg.Store.UpdateSAServerPlatformAudited(r.Context(), server.TenantID, id, server.Name, body.Status,
		body.VerifyRateLimit, body.VerifyBurst, body.MaxConcurrent, body.WhitelistLimit, body.EventRetentionDays, who.UID); err != nil {
		writeSAStoreError(w, err)
		return
	}
	writeSAJSON(w, http.StatusOK, map[string]any{"id": id})
}

func (c *control) platformRevoke(w http.ResponseWriter, r *http.Request) {
	serverID, ok := saID(r, "id")
	credentialID, credentialOK := saID(r, "cid")
	if !ok || !credentialOK {
		writeSAError(w, http.StatusBadRequest, i18n.MsgBadID, "Invalid server or credential id")
		return
	}
	server, err := c.cfg.Store.PlatformSAServerByID(r.Context(), serverID)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	who := saCaller(r)
	if err := c.cfg.Store.RevokeSAServerCredentialAudited(r.Context(), server.TenantID, serverID, credentialID, who.UID,
		"serveraccess_platform_credential_revoke", map[string]any{"tenantId": server.TenantID, "serverId": serverID, "credentialId": credentialID}); err != nil {
		writeSAStoreError(w, err)
		return
	}
	writeSAJSON(w, http.StatusOK, map[string]any{"revoked": true})
}

func (c *control) platformFallback(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		raw, ok, err := c.cfg.Store.GetOption(r.Context(), serveraccessGlobalFallbackOption)
		if err != nil {
			writeSAStoreError(w, err)
			return
		}
		enabled := ok && (raw == "1" || raw == "true")
		writeSAJSON(w, http.StatusOK, map[string]any{"enabled": enabled})
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeSAJSON(w, r, &body) || body.Enabled == nil {
		writeSAError(w, http.StatusBadRequest, i18n.MsgServerAccessInvalid, "enabled must be a boolean")
		return
	}
	value := "0"
	if *body.Enabled {
		value = "1"
	}
	if err := c.cfg.Store.SetOptionAudited(r.Context(), serveraccessGlobalFallbackOption, value, saCaller(r).UID,
		"serveraccess_fallback_update", map[string]any{"enabled": *body.Enabled}); err != nil {
		writeSAStoreError(w, err)
		return
	}
	writeSAJSON(w, http.StatusOK, map[string]any{"enabled": *body.Enabled})
}

func (c *control) platformAudit(w http.ResponseWriter, r *http.Request) {
	page, size := saPage(r)
	rows, total, err := c.cfg.Store.PlatformServerAccessAudit(r.Context(), r.URL.Query().Get("q"), page, size)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	writeSAJSON(w, http.StatusOK, map[string]any{"audit": rows, "total": total, "page": page, "size": size})
}

func saPage(r *http.Request) (int, int) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 20
	}
	if size > 200 {
		size = 200
	}
	return page, size
}
