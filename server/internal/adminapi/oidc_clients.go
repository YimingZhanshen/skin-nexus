// oidc_clients.go: admin CRUD for registered OAuth clients (phase-2 L3).
// Secrets are argon2id PHC hashes (same pipeline as passwords); the plaintext
// is echoed exactly once in the create response and never stored.
// The shared launcher client (discovery shared_client_id) cannot be deleted.
package adminapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"

	"skin-nexus/server/internal/auth"
	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/oidc"
)

// SharedClientID is set from main (cfg.SharedClientID) and guards deletion.
func (b *Backend) SetSharedClientID(id string) { b.sharedClientID = id }

func genClientSecret() string {
	buf := make([]byte, 32)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

// GET /api/admin/v1/oidc/clients — list registered clients (no hashes).
func (b *Backend) handleOIDCClientsList(w http.ResponseWriter, r *http.Request) {
	rows, err := b.Store.ListClients(r.Context())
	if err != nil {
		writeInternal(r, w, err)
		return
	}
	type item struct {
		ID           string   `json:"client_id"`
		Name         string   `json:"name"`
		RedirectURIs []string `json:"redirect_uris"`
		GrantTypes   []string `json:"grant_types"`
		Scopes       []string `json:"scopes"`
		Public       bool     `json:"public"`
	}
	out := make([]item, 0, len(rows))
	for _, c := range rows {
		out = append(out, item{
			ID: c.ID, Name: c.Name, RedirectURIs: c.RedirectURIs,
			GrantTypes: c.GrantTypes, Scopes: c.Scopes, Public: c.SecretHash == "",
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"clients": out})
}

// POST /api/admin/v1/oidc/clients — create/upsert a client.
// Body: {client_id, name, redirect_uris, grant_types, scopes, public, secret?}.
// secret is generated when omitted and a confidential client is requested;
// the plaintext appears ONLY in this response.
func (b *Backend) handleOIDCClientsCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID           string   `json:"client_id"`
		Name         string   `json:"name"`
		RedirectURIs []string `json:"redirect_uris"`
		GrantTypes   []string `json:"grant_types"`
		Scopes       []string `json:"scopes"`
		Public       bool     `json:"public"`
		Secret       string   `json:"secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.ID == "" {
		writeErrCode(w, http.StatusBadRequest, "bad body", i18n.MsgBadBody)
		return
	}
	rec := oidc.ClientRecord{
		ID: body.ID, Name: body.Name,
		RedirectURIs: body.RedirectURIs, GrantTypes: body.GrantTypes, Scopes: body.Scopes,
	}
	plain := ""
	if !body.Public {
		plain = body.Secret
		if plain == "" {
			plain = genClientSecret()
		}
		hash, err := auth.HashArgon2id(plain)
		if err != nil {
			writeErrCode(w, http.StatusInternalServerError, "hash failed", i18n.MsgInternal)
			return
		}
		rec.SecretHash = hash
	}
	if err := b.Store.EnsureOidcClient(r.Context(), rec); err != nil {
		writeInternal(r, w, err)
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "oidc_client_upsert", map[string]any{
		"client_id": body.ID, "public": body.Public,
	})
	resp := map[string]any{"ok": true, "client_id": body.ID}
	if plain != "" {
		resp["secret"] = plain // shown once — store nowhere else
	}
	writeJSON(w, http.StatusOK, resp)
}

// DELETE /api/admin/v1/oidc/clients/{id} — remove a client (shared client guarded).
func (b *Backend) handleOIDCClientsDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		writeErrCode(w, http.StatusBadRequest, "missing id", i18n.MsgMissingField)
		return
	}
	if id == b.sharedClientID {
		writeErrCode(w, http.StatusForbidden, "shared launcher client cannot be deleted", i18n.MsgOIDCSharedClient)
		return
	}
	if err := b.Store.DeleteClient(r.Context(), id); err != nil {
		writeInternal(r, w, err)
		return
	}
	b.Store.InsertAudit(r.Context(), actorUID(r), "oidc_client_delete", map[string]any{"client_id": id})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
