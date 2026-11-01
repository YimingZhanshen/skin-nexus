package adminapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/plugin"
)

// PluginsDeps wires the plugin host into the admin surface. Mounting is
// optional: without it the /plugins routes do not exist (zero surface).
type PluginsDeps struct {
	Host *plugin.Manager
}

// actorPermission resolves an admin actor's legacy permission. Overridable in
// tests (same seam style as sessionAdmin) because *store.Mysql is concrete.
var actorPermission = func(ctx context.Context, b *Backend, uid int64) (int, error) {
	return b.Store.UserPermission(ctx, uid)
}

// MountPlugins registers the plugin management routes.
func (b *Backend) MountPlugins(deps PluginsDeps) {
	b.plugins = &deps
}

func (b *Backend) handlePluginsList(w http.ResponseWriter, r *http.Request) {
	list := b.plugins.Host.List()
	if list == nil {
		list = []plugin.Status{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"plugins": list, "guardsBypassed": b.plugins.Host.GuardsBypassed(r.Context()),
	})
}

// handlePluginAction serves enable/disable/reload. Enabling executes third
// party code in-process, so it mirrors handleUserSetPermission: session
// SUPER_ADMIN (permission=2) only, and the X-Admin-Token machine path is
// refused outright.
func (b *Backend) handlePluginAction(w http.ResponseWriter, r *http.Request) {
	actor := actorUID(r)
	if actor == 0 {
		writeErrCode(w, http.StatusForbidden, "插件操作需管理员会话（不支持 X-Admin-Token）", i18n.MsgWebSessionRequired)
		return
	}
	perm, err := actorPermission(r.Context(), b, actor)
	if err != nil || perm != 2 {
		writeErrCode(w, http.StatusForbidden, "插件操作需要超级管理员（permission=2）账号", i18n.MsgSuperAdminRequired)
		return
	}
	name := r.PathValue("name")
	action := r.PathValue("action")

	ctx := plugin.WithActor(r.Context(), actor)
	var opErr error
	switch action {
	case "enable":
		opErr = b.plugins.Host.Enable(ctx, name)
	case "disable":
		opErr = b.plugins.Host.Disable(ctx, name)
	case "reload":
		opErr = b.plugins.Host.Reload(ctx, name)
	default:
		writeErrCode(w, http.StatusNotFound, "unknown action", i18n.MsgUnknownAction)
		return
	}

	switch {
	case errors.Is(opErr, plugin.ErrPluginNotFound):
		writeErrCode(w, http.StatusNotFound, "plugin not found", i18n.MsgPluginNotFound)
	case opErr != nil:
		writeLogged(r, w, http.StatusBadGateway, i18n.MsgPluginOpFailed, "plugin operation failed", opErr)
	default:
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}

func (b *Backend) handlePluginConfigGet(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	view, err := b.plugins.Host.PluginConfig(name)
	switch {
	case errors.Is(err, plugin.ErrPluginNotFound):
		writeErrCode(w, http.StatusNotFound, "plugin not found", i18n.MsgPluginNotFound)
	case errors.Is(err, plugin.ErrPluginNoConfig):
		writeErrCode(w, http.StatusConflict, "plugin has no config", i18n.MsgPluginNoConfig)
	case err != nil:
		writeInternal(r, w, err)
	default:
		writeJSON(w, http.StatusOK, view)
	}
}

type pluginConfigBody struct {
	Values map[string]any `json:"values"`
}

func (b *Backend) handlePluginConfigSave(w http.ResponseWriter, r *http.Request) {
	actor := actorUID(r)
	if actor == 0 {
		writeErrCode(w, http.StatusForbidden, "配置修改需超级管理员会话（不支持 X-Admin-Token）", i18n.MsgWebSessionRequired)
		return
	}
	perm, err := actorPermission(r.Context(), b, actor)
	if err != nil || perm != 2 {
		writeErrCode(w, http.StatusForbidden, "配置修改需要超级管理员（permission=2）账号", i18n.MsgSuperAdminRequired)
		return
	}
	var body pluginConfigBody
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON body", i18n.MsgBadBody)
		return
	}
	ctx := plugin.WithActor(r.Context(), actor)
	view, err := b.plugins.Host.SavePluginConfig(ctx, r.PathValue("name"), body.Values)
	switch {
	case errors.Is(err, plugin.ErrPluginNotFound):
		writeErrCode(w, http.StatusNotFound, "plugin not found", i18n.MsgPluginNotFound)
	case errors.Is(err, plugin.ErrPluginNoConfig):
		writeErrCode(w, http.StatusConflict, "plugin has no config", i18n.MsgPluginNoConfig)
	case errors.Is(err, plugin.ErrPluginNoStore), errors.Is(err, plugin.ErrPluginNoSecrets):
		// The manifest is the reason — it declares no store or no secrets, so
		// there is nowhere to put what was typed. The text says which.
		writeDomain(w, http.StatusConflict, err, i18n.MsgPluginNoConfig)
	case err != nil:
		var ce *plugin.ConfigError
		if errors.As(err, &ce) {
			writeDomain(w, http.StatusBadRequest, ce, i18n.MsgInvalidValue)
			return
		}
		writeLogged(r, w, http.StatusBadRequest, i18n.MsgPluginOpFailed, "config rejected", err)
	default:
		writeJSON(w, http.StatusOK, view)
	}
}

func (b *Backend) handlePluginGuardBypass(w http.ResponseWriter, r *http.Request) {
	actor := actorUID(r)
	if actor == 0 {
		writeErrCode(w, http.StatusForbidden, "破窗操作需超级管理员会话", i18n.MsgWebSessionRequired)
		return
	}
	perm, err := actorPermission(r.Context(), b, actor)
	if err != nil || perm != 2 {
		writeErrCode(w, http.StatusForbidden, "破窗操作需要超级管理员（permission=2）账号", i18n.MsgSuperAdminRequired)
		return
	}
	var body struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErrCode(w, http.StatusBadRequest, "invalid JSON body", i18n.MsgBadBody)
		return
	}
	ctx := plugin.WithActor(r.Context(), actor)
	if err := b.plugins.Host.SetGuardsBypassed(ctx, body.Enabled); err != nil {
		writeLogged(r, w, http.StatusBadGateway, i18n.MsgPluginOpFailed, "plugin operation failed", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"guardsBypassed": body.Enabled})
}
