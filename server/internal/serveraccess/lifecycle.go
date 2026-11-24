package serveraccess

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/store"
)

type saExportTenant struct {
	ID                  int64      `json:"id"`
	Name                string     `json:"name"`
	Status              string     `json:"status"`
	ServerLimit         int        `json:"serverLimit"`
	CreatedAt           time.Time  `json:"createdAt"`
	DeletionRequestedAt *time.Time `json:"deletionRequestedAt"`
	DeletionDueAt       *time.Time `json:"deletionDueAt"`
}

type saExportPolicy struct {
	WhitelistEnabled       bool `json:"whitelistEnabled"`
	PremiumFallbackEnabled bool `json:"premiumFallbackEnabled"`
	VerifyRateLimit        int  `json:"verifyRateLimit"`
	VerifyBurst            int  `json:"verifyBurst"`
	MaxConcurrent          int  `json:"maxConcurrent"`
	WhitelistLimit         int  `json:"whitelistLimit"`
	EventRetentionDays     int  `json:"eventRetentionDays"`
}

type saExportWhitelist struct {
	UID       int64     `json:"uid"`
	Note      string    `json:"note"`
	CreatedAt time.Time `json:"createdAt"`
}

type saExportBan struct {
	UID       int64      `json:"uid"`
	Reason    string     `json:"reason"`
	ExpiresAt *time.Time `json:"expiresAt"`
	CreatedAt time.Time  `json:"createdAt"`
}

type saExportEvent struct {
	ID         int64     `json:"id"`
	CreatedAt  time.Time `json:"createdAt"`
	Outcome    string    `json:"outcome"`
	ReasonCode string    `json:"reasonCode"`
	DurationMS int64     `json:"durationMs"`
	SourceMode string    `json:"sourceMode"`
}

type saExportCredential struct {
	ID         int64      `json:"id"`
	Mode       string     `json:"mode"`
	Status     string     `json:"status"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	ExpiresAt  *time.Time `json:"expiresAt"`
}

type saExportServer struct {
	ID          int64                `json:"id"`
	PublicID    string               `json:"publicId"`
	Name        string               `json:"name"`
	Status      string               `json:"status"`
	SourceMode  string               `json:"sourceMode"`
	CreatedAt   time.Time            `json:"createdAt"`
	Policy      saExportPolicy       `json:"policy"`
	Whitelist   []saExportWhitelist  `json:"whitelist"`
	Bans        []saExportBan        `json:"bans"`
	Events      []saExportEvent      `json:"events"`
	Credentials []saExportCredential `json:"credentials"`
}

type saExportDocument struct {
	SchemaVersion int              `json:"schemaVersion"`
	ExportedAt    time.Time        `json:"exportedAt"`
	Tenant        saExportTenant   `json:"tenant"`
	Servers       []saExportServer `json:"servers"`
}

func (c *control) tenantExport(w http.ResponseWriter, r *http.Request) {
	who := saCaller(r)
	tenant, err := c.cfg.Store.SATenantByID(r.Context(), who.TenantID)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	servers, err := c.cfg.Store.ListSAServersForExport(r.Context(), who.TenantID)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	document := saExportDocument{
		SchemaVersion: 1,
		ExportedAt:    c.now(),
		Tenant: saExportTenant{
			ID: tenant.ID, Name: tenant.Name, Status: tenant.Status, ServerLimit: tenant.ServerLimit,
			CreatedAt: tenant.CreatedAt, DeletionRequestedAt: tenant.DeletionRequestedAt, DeletionDueAt: tenant.DeletionDueAt,
		},
		Servers: make([]saExportServer, 0, len(servers)),
	}
	for _, server := range servers {
		policy, err := c.cfg.Store.SAServerPolicy(r.Context(), who.TenantID, server.ID)
		if err != nil {
			writeSAStoreError(w, err)
			return
		}
		whitelist, err := c.cfg.Store.ListSAServerWhitelistForExport(r.Context(), who.TenantID, server.ID)
		if err != nil {
			writeSAStoreError(w, err)
			return
		}
		bans, err := c.cfg.Store.ListSAServerBansForExport(r.Context(), who.TenantID, server.ID)
		if err != nil {
			writeSAStoreError(w, err)
			return
		}
		events, err := c.cfg.Store.ListSAOwnerJoinEventsForExport(r.Context(), who.TenantID, server.ID)
		if err != nil {
			writeSAStoreError(w, err)
			return
		}
		credentials, err := c.cfg.Store.ListSAServerCredentials(r.Context(), who.TenantID, server.ID)
		if err != nil {
			writeSAStoreError(w, err)
			return
		}
		item := saExportServer{
			ID: server.ID, PublicID: server.PublicID, Name: server.Name, Status: server.Status,
			SourceMode: server.SourceMode, CreatedAt: server.CreatedAt,
			Policy: saExportPolicy{
				WhitelistEnabled: policy.WhitelistEnabled, PremiumFallbackEnabled: policy.PremiumFallbackEnabled,
				VerifyRateLimit: policy.VerifyRateLimit, VerifyBurst: policy.VerifyBurst, MaxConcurrent: policy.MaxConcurrent,
				WhitelistLimit: policy.WhitelistLimit, EventRetentionDays: policy.EventRetentionDays,
			},
			Whitelist:   make([]saExportWhitelist, 0, len(whitelist)),
			Bans:        make([]saExportBan, 0, len(bans)),
			Events:      make([]saExportEvent, 0, len(events)),
			Credentials: make([]saExportCredential, 0, len(credentials)),
		}
		for _, row := range whitelist {
			item.Whitelist = append(item.Whitelist, saExportWhitelist{UID: row.UID, Note: row.Note, CreatedAt: row.CreatedAt})
		}
		for _, row := range bans {
			item.Bans = append(item.Bans, saExportBan{UID: row.UID, Reason: row.Reason, ExpiresAt: saTimePointer(row.ExpiresAt), CreatedAt: row.CreatedAt})
		}
		for _, row := range events {
			item.Events = append(item.Events, saExportEvent{
				ID: row.ID, CreatedAt: row.CreatedAt, Outcome: row.Outcome,
				ReasonCode: row.ReasonCode, DurationMS: row.DurationMS, SourceMode: row.SourceMode,
			})
		}
		for _, row := range credentials {
			item.Credentials = append(item.Credentials, saExportCredential{
				ID: row.ID, Mode: row.Mode, Status: row.Status, CreatedAt: row.CreatedAt,
				LastUsedAt: saTimePointer(row.LastUsedAt), ExpiresAt: saTimePointer(row.ExpiresAt),
			})
		}
		document.Servers = append(document.Servers, item)
	}
	c.cfg.Store.InsertAudit(r.Context(), who.UID, "serveraccess_tenant_export", map[string]any{"tenantId": who.TenantID})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="serveraccess-tenant-%d.json"`, who.TenantID))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(document)
}

func (c *control) tenantDeleteRequest(w http.ResponseWriter, r *http.Request) {
	if r.ContentLength != 0 {
		var body struct{}
		if !decodeSAJSON(w, r, &body) {
			return
		}
	}
	who := saCaller(r)
	tenant, err := c.cfg.Store.RequestSATenantDeletion(r.Context(), who.TenantID, c.cfg.DeletionGracePeriod, c.now())
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	c.cfg.Store.InsertAudit(r.Context(), who.UID, "serveraccess_tenant_delete_request", map[string]any{
		"tenantId": who.TenantID, "deletionDueAt": tenant.DeletionDueAt.Format(time.RFC3339),
	})
	writeSAJSON(w, http.StatusOK, map[string]any{
		"status": tenant.Status, "deletionRequestedAt": tenant.DeletionRequestedAt, "deletionDueAt": tenant.DeletionDueAt,
	})
}

func (c *control) platformTenantDelete(w http.ResponseWriter, r *http.Request) {
	id, ok := saID(r, "id")
	if !ok {
		writeSAError(w, http.StatusBadRequest, i18n.MsgBadID, "Invalid tenant id")
		return
	}
	if err := c.cfg.Store.DeleteDueSATenant(r.Context(), id, c.now()); err != nil {
		if errors.Is(err, store.ErrDeletionNotDue) {
			writeSAError(w, http.StatusConflict, i18n.MsgServerAccessConflict, "Tenant deletion is not requested or its grace period has not elapsed")
		} else {
			writeSAStoreError(w, err)
		}
		return
	}
	c.cfg.Store.InsertAudit(r.Context(), saCaller(r).UID, "serveraccess_tenant_delete", map[string]any{"tenantId": id})
	writeSAJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func saTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}
