package serveraccess

import (
	"bytes"
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
	limits := store.ResolveSAExportLimits(c.cfg.ExportLimits)
	data, err := c.cfg.Store.SAExportData(r.Context(), who.TenantID, limits)
	if err != nil {
		if errors.Is(err, store.ErrSAExportTooLarge) {
			var limitErr *store.SAExportLimitError
			if errors.As(err, &limitErr) {
				writeSAError(w, http.StatusRequestEntityTooLarge, i18n.MsgServerAccessExportTooLarge, limitErr.Error())
			} else {
				writeSAError(w, http.StatusRequestEntityTooLarge, i18n.MsgServerAccessExportTooLarge, "Tenant export exceeds its explicit size limit")
			}
		} else {
			writeSAStoreError(w, err)
		}
		return
	}
	tenant, err := c.cfg.Store.SATenantByID(r.Context(), who.TenantID)
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
		Servers: make([]saExportServer, 0, len(data.Servers)),
	}
	for _, server := range data.Servers {
		policy, ok := data.Policies[server.ID]
		if !ok {
			writeSAStoreError(w, store.ErrNotFound)
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
			Whitelist:   make([]saExportWhitelist, 0, len(data.Whitelist[server.ID])),
			Bans:        make([]saExportBan, 0, len(data.Bans[server.ID])),
			Events:      make([]saExportEvent, 0, len(data.Events[server.ID])),
			Credentials: make([]saExportCredential, 0, len(data.Credentials[server.ID])),
		}
		for _, row := range data.Whitelist[server.ID] {
			item.Whitelist = append(item.Whitelist, saExportWhitelist{UID: row.UID, Note: row.Note, CreatedAt: row.CreatedAt})
		}
		for _, row := range data.Bans[server.ID] {
			item.Bans = append(item.Bans, saExportBan{UID: row.UID, Reason: row.Reason, ExpiresAt: saTimePointer(row.ExpiresAt), CreatedAt: row.CreatedAt})
		}
		for _, row := range data.Events[server.ID] {
			item.Events = append(item.Events, saExportEvent{
				ID: row.ID, CreatedAt: row.CreatedAt, Outcome: row.Outcome,
				ReasonCode: row.ReasonCode, DurationMS: row.DurationMS, SourceMode: row.SourceMode,
			})
		}
		for _, row := range data.Credentials[server.ID] {
			item.Credentials = append(item.Credentials, saExportCredential{
				ID: row.ID, Mode: row.Mode, Status: row.Status, CreatedAt: row.CreatedAt,
				LastUsedAt: saTimePointer(row.LastUsedAt), ExpiresAt: saTimePointer(row.ExpiresAt),
			})
		}
		document.Servers = append(document.Servers, item)
	}
	writer := &saExportByteWriter{remaining: limits.MaxBytes, limit: limits.MaxBytes}
	if err := json.NewEncoder(writer).Encode(document); err != nil {
		if errors.Is(err, store.ErrSAExportTooLarge) {
			writeSAError(w, http.StatusRequestEntityTooLarge, i18n.MsgServerAccessExportTooLarge,
				fmt.Sprintf("bytes exceeds export limit of %d", limits.MaxBytes))
		} else {
			writeSAStoreError(w, err)
		}
		return
	}
	// The encoder above writes into a bounded buffer, not the HTTP response.
	// Copy only after the complete document is known to fit.
	c.cfg.Store.InsertAudit(r.Context(), who.UID, "serveraccess_tenant_export", map[string]any{"tenantId": who.TenantID})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="serveraccess-tenant-%d.json"`, who.TenantID))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(writer.Bytes())
}

type saExportByteWriter struct {
	bytes.Buffer
	remaining int
	limit     int
}

func (w *saExportByteWriter) Write(p []byte) (int, error) {
	if len(p) > w.remaining {
		return 0, &store.SAExportLimitError{Collection: "bytes", Limit: int64(w.limit)}
	}
	w.remaining -= len(p)
	return w.Buffer.Write(p)
}

func (c *control) tenantDeleteRequest(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Confirmation string `json:"confirmation"`
	}
	if !decodeSAJSON(w, r, &body) {
		return
	}
	who := saCaller(r)
	tenant, err := c.cfg.Store.RequestSATenantDeletionConfirmed(r.Context(), who.TenantID, body.Confirmation, c.cfg.DeletionGracePeriod, c.now())
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
	var body struct {
		Confirmation string `json:"confirmation"`
	}
	if !decodeSAJSON(w, r, &body) {
		return
	}
	if err := c.cfg.Store.DeleteDueSATenantConfirmedAudited(r.Context(), id, body.Confirmation, c.now(), saCaller(r).UID,
		"serveraccess_tenant_delete", map[string]any{"tenantId": id}); err != nil {
		if errors.Is(err, store.ErrDeletionNotDue) {
			writeSAError(w, http.StatusConflict, i18n.MsgServerAccessConflict, "Tenant deletion is not requested or its grace period has not elapsed")
		} else {
			writeSAStoreError(w, err)
		}
		return
	}
	writeSAJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

func saTimePointer(value sql.NullTime) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}
