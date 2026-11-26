package serveraccess

import (
	"database/sql"
	"net/http"
	"time"

	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/store"
)

type saCredentialView struct {
	store.SACredential
	Scopes []string `json:"scopes"`
}

func saViewCredential(c store.SACredential) saCredentialView {
	return saCredentialView{SACredential: c, Scopes: saScopeJSON(c.Scopes)}
}

func (c *control) credentials(w http.ResponseWriter, r *http.Request) {
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
	rows, err := c.cfg.Store.ListSAServerCredentials(r.Context(), who.TenantID, id)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	view := make([]saCredentialView, 0, len(rows))
	for _, row := range rows {
		view = append(view, saViewCredential(row))
	}
	writeSAJSON(w, http.StatusOK, map[string]any{"credentials": view})
}

func (c *control) createCredential(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if !decodeSAJSON(w, r, &body) {
		return
	}
	if body.Mode != "url_secret" && body.Mode != "header" {
		writeSAError(w, http.StatusBadRequest, i18n.MsgServerAccessInvalid, "Credential mode must be url_secret or header")
		return
	}
	serverID, ok := saID(r, "id")
	if !ok {
		writeSAError(w, http.StatusBadRequest, i18n.MsgBadID, "Invalid server id")
		return
	}
	who := saCaller(r)
	if _, err := c.cfg.Store.SAServerByID(r.Context(), who.TenantID, serverID); err != nil {
		writeSAStoreError(w, err)
		return
	}
	secret, credential, err := c.newCredential(body.Mode)
	if err != nil {
		writeSAError(w, http.StatusInternalServerError, i18n.MsgInternal, "Could not create credential")
		return
	}
	credential.ID, err = c.cfg.Store.CreateSAServerCredential(r.Context(), who.TenantID, serverID, credential)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	credential.ServerID = serverID
	c.cfg.Store.InsertAudit(r.Context(), who.UID, "serveraccess_credential_create",
		map[string]any{"tenantId": who.TenantID, "serverId": serverID, "credentialId": credential.ID, "mode": body.Mode})
	saCredentialJSON(w, http.StatusCreated, credential, secret)
}

func (c *control) rotateCredential(w http.ResponseWriter, r *http.Request) {
	serverID, ok := saID(r, "id")
	credentialID, cidOK := saID(r, "cid")
	if !ok || !cidOK {
		writeSAError(w, http.StatusBadRequest, i18n.MsgBadID, "Invalid server or credential id")
		return
	}
	var body struct {
		Mode string `json:"mode"`
	}
	if !decodeSAJSON(w, r, &body) {
		return
	}
	who := saCaller(r)
	rows, err := c.cfg.Store.ListSAServerCredentials(r.Context(), who.TenantID, serverID)
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	mode := body.Mode
	if mode == "" {
		for _, row := range rows {
			if row.ID == credentialID {
				mode = row.Mode
				break
			}
		}
	}
	if mode != "url_secret" && mode != "header" {
		writeSAError(w, http.StatusNotFound, i18n.MsgServerAccessNotFound, "Credential not found")
		return
	}
	secret, fresh, err := c.newCredential(mode)
	if err != nil {
		writeSAError(w, http.StatusInternalServerError, i18n.MsgInternal, "Could not rotate credential")
		return
	}
	overlap := c.cfg.RotationOverlap
	if overlap <= 0 {
		overlap = 24 * time.Hour
	}
	fresh.ID, err = c.cfg.Store.RotateSAServerCredential(r.Context(), who.TenantID, serverID, credentialID, fresh, overlap, c.now())
	if err != nil {
		writeSAStoreError(w, err)
		return
	}
	fresh.ServerID = serverID
	c.cfg.Store.InsertAudit(r.Context(), who.UID, "serveraccess_credential_rotate",
		map[string]any{"tenantId": who.TenantID, "serverId": serverID, "oldCredentialId": credentialID, "credentialId": fresh.ID})
	saCredentialJSON(w, http.StatusCreated, fresh, secret)
}

func (c *control) revokeCredential(w http.ResponseWriter, r *http.Request) {
	serverID, credentialID, tenantID, ok := c.revoke(w, r)
	if !ok {
		return
	}
	who := saCaller(r)
	c.cfg.Store.InsertAudit(r.Context(), who.UID, "serveraccess_credential_revoke",
		map[string]any{"tenantId": tenantID, "serverId": serverID, "credentialId": credentialID})
	writeSAJSON(w, http.StatusOK, map[string]any{"revoked": true})
}

func (c *control) newCredential(mode string) (string, store.SACredential, error) {
	secret, err := saRandomToken()
	if err != nil {
		return "", store.SACredential{}, err
	}
	ttl := c.cfg.CredentialTTL
	if ttl <= 0 {
		ttl = 90 * 24 * time.Hour
	}
	return secret, store.SACredential{
		ServerID: 0, Mode: mode, SecretHash: saHash(secret), Scopes: `["verify:join"]`,
		Status: "active", ExpiresAt: sql.NullTime{Time: c.now().Add(ttl), Valid: true}, CreatedAt: c.now(),
	}, nil
}

func (c *control) revoke(w http.ResponseWriter, r *http.Request) (int64, int64, int64, bool) {
	serverID, ok := saID(r, "id")
	credentialID, cidOK := saID(r, "cid")
	if !ok || !cidOK {
		writeSAError(w, http.StatusBadRequest, i18n.MsgBadID, "Invalid server or credential id")
		return 0, 0, 0, false
	}
	who := saCaller(r)
	if who.TenantID == 0 {
		server, err := c.cfg.Store.PlatformSAServerByID(r.Context(), serverID)
		if err != nil {
			writeSAStoreError(w, err)
			return 0, 0, 0, false
		}
		who.TenantID = server.TenantID
	}
	if err := c.cfg.Store.RevokeSAServerCredential(r.Context(), who.TenantID, serverID, credentialID, c.now()); err != nil {
		writeSAStoreError(w, err)
		return 0, 0, 0, false
	}
	return serverID, credentialID, who.TenantID, true
}
