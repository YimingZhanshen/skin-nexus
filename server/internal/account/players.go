package account

import (
	"context"
	"errors"
	"os"
	"strings"

	"skin-nexus/server/internal/plugin"
	"skin-nexus/server/internal/siteurl"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/ygg"
)

func (s *Service) playerLimit(ctx context.Context, uid int64) int {
	limit := s.MaxChars
	if limit <= 0 {
		limit = 3
	}
	if n, err := s.Store.UserPlayerLimit(ctx, uid); err == nil && n > 0 {
		limit = n
	}
	return limit
}

func (s *Service) ownershipError() *Error {
	return errf(CodeNotFound, "player not found", nil)
}

func (s *Service) guard(ctx context.Context, kind string, p plugin.EventPayload) *Error {
	if s.CheckGuard == nil {
		return nil
	}
	if err := s.CheckGuard(ctx, kind, p); err != nil {
		return errDomain(CodeForbidden, err)
	}
	return nil
}

func (s *Service) audit(ctx context.Context, uid int64, meta CallMeta, webAction, pluginAction string, detail map[string]any) {
	if s.Audit == nil {
		return
	}
	action := webAction
	if meta.Plugin != "" {
		action = pluginAction
		if detail == nil {
			detail = map[string]any{}
		}
		detail["via"] = "plugin"
		detail["plugin"] = meta.Plugin
		detail["pluginVersion"] = meta.Version
		detail["signer"] = meta.Signer
	}
	if action == "" {
		return
	}
	s.Audit(ctx, uid, action, detail)
}

func (s *Service) ListPlayers(ctx context.Context, uid int64) ([]Player, *Error) {
	if e := s.ensure(); e != nil {
		return nil, e
	}
	rows, err := s.Store.PlayersByUID(ctx, uid)
	if err != nil {
		return nil, errf(CodeInternal, "could not read the player", err)
	}
	out := make([]Player, 0, len(rows))
	for _, p := range rows {
		out = append(out, s.playerRow(ctx, p))
	}
	return out, nil
}

func (s *Service) playerRow(ctx context.Context, p ygg.Player) Player {
	skinTID, capeTID, err := s.Store.PlayerTextureIDs(ctx, p.ID)
	if err != nil {
		skinTID, capeTID = 0, 0
	}
	return Player{
		ID: p.ID, UID: p.UID, Name: p.Name, UUID: p.UUID,
		SkinTID: skinTID, CapeTID: capeTID,
		SkinHash: p.SkinHash, CapeHash: p.CapeHash, SkinModel: p.SkinModel,
	}
}

func (s *Service) GetPlayer(ctx context.Context, uid, pid int64) (Player, *Error) {
	list, e := s.ListPlayers(ctx, uid)
	if e != nil {
		return Player{}, e
	}
	for _, p := range list {
		if p.ID == pid {
			return p, nil
		}
	}
	return Player{}, s.ownershipError()
}

func (s *Service) PlayerView(ctx context.Context, uid, pid int64) (PlayerView, *Error) {
	p, e := s.GetPlayer(ctx, uid, pid)
	if e != nil {
		return PlayerView{}, e
	}
	return s.view(p), nil
}

// PlayerViewOf exposes the URL projection for callers that already hold a row.
func (s *Service) PlayerViewOf(p Player) PlayerView { return s.view(p) }

func (s *Service) view(p Player) PlayerView {
	v := PlayerView{ID: p.ID, Name: p.Name, UUID: p.UUID}
	if p.SkinTID != 0 && p.SkinHash != "" {
		v.Skin = &TextureView{
			ID: p.SkinTID, Type: "skin", Hash: p.SkinHash, Model: p.SkinModel,
			URL: s.textureURL(p.SkinHash), PreviewURL: s.previewURL(p.SkinTID, "skin"),
			AvatarURL: s.avatarURL(p.SkinHash),
		}
	}
	if p.CapeTID != 0 && p.CapeHash != "" {
		v.Cape = &TextureView{
			ID: p.CapeTID, Type: "cape", Hash: p.CapeHash,
			URL: s.textureURL(p.CapeHash),
		}
	}
	return v
}

func (s *Service) CreatePlayer(ctx context.Context, uid int64, name string, meta CallMeta) (PlayerView, bool, *Error) {
	if e := s.ensure(); e != nil {
		return PlayerView{}, false, e
	}
	name = strings.TrimSpace(name)
	if err := s.Store.ValidatePlayerName(ctx, name); err != nil {
		return PlayerView{}, false, errDomain(CodeInvalidName, err)
	}
	existing, err := s.Store.PlayersByUID(ctx, uid)
	if err != nil {
		return PlayerView{}, false, errf(CodeInternal, "could not read the player", err)
	}
	if len(existing) >= s.playerLimit(ctx, uid) {
		return PlayerView{}, false, errf(CodeLimit, "player limit reached", nil)
	}
	pid, uuid, err := s.Store.CreatePlayer(ctx, uid, name)
	if err != nil {
		if p, qerr := s.Store.PlayerByName(ctx, name); qerr == nil && p.UID == uid {
			v, e := s.PlayerView(ctx, uid, p.ID)
			return v, false, e
		}
		if errors.Is(err, store.ErrDuplicate) {
			return PlayerView{}, false, errf(CodeNameTaken, "player name already taken", err)
		}
		return PlayerView{}, false, errf(CodeInternal, "could not create the player", err)
	}
	s.audit(ctx, uid, meta, "", "plugin_player_create", map[string]any{"pid": pid, "name": name})
	if s.EmitEvent != nil {
		s.EmitEvent(ctx, "player.added", plugin.EventPayload{
			UserID: uid, PlayerID: pid, PlayerName: name, UUID: uuid, IP: meta.IP,
		})
	}
	if s.UnionSync != nil {
		s.UnionSync(ctx, uuid, name)
	}
	v, e := s.PlayerView(ctx, uid, pid)
	return v, true, e
}

func (s *Service) RenamePlayer(ctx context.Context, uid, pid int64, name string, meta CallMeta) (PlayerView, bool, *Error) {
	if e := s.ensure(); e != nil {
		return PlayerView{}, false, e
	}
	name = strings.TrimSpace(name)
	if err := s.Store.ValidatePlayerName(ctx, name); err != nil {
		return PlayerView{}, false, errDomain(CodeInvalidName, err)
	}
	p, e := s.GetPlayer(ctx, uid, pid)
	if e != nil {
		return PlayerView{}, false, e
	}
	if p.Name == name {
		v, e := s.PlayerView(ctx, uid, pid)
		return v, false, e
	}
	if ge := s.guard(ctx, "player.rename.check", plugin.EventPayload{
		UserID: uid, PlayerID: pid, PlayerName: name, IP: meta.IP,
		Data: map[string]any{"oldName": p.Name},
	}); ge != nil {
		return PlayerView{}, false, ge
	}
	if err := s.Store.AdminRenamePlayer(ctx, pid, name); err != nil {
		if errors.Is(err, store.ErrDuplicate) {
			return PlayerView{}, false, errf(CodeNameTaken, "player name already taken", err)
		}
		if errors.Is(err, store.ErrNotFound) {
			return PlayerView{}, false, s.ownershipError()
		}
		return PlayerView{}, false, errf(CodeInternal, "could not rename the player", err)
	}
	s.audit(ctx, uid, meta, "player_rename_self", "plugin_player_rename", map[string]any{
		"pid": pid, "name": name, "oldName": p.Name,
	})
	if s.EmitEvent != nil {
		s.EmitEvent(ctx, "player.renamed", plugin.EventPayload{
			UserID: uid, PlayerID: pid, PlayerName: name, IP: meta.IP,
			Data: map[string]any{"oldName": p.Name},
		})
	}
	v, e := s.PlayerView(ctx, uid, pid)
	return v, true, e
}

func (s *Service) DeletePlayer(ctx context.Context, uid, pid int64, meta CallMeta) *Error {
	if e := s.ensure(); e != nil {
		return e
	}
	if _, e := s.GetPlayer(ctx, uid, pid); e != nil {
		return e
	}
	uuid, name, err := s.Store.AdminDeletePlayer(ctx, pid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return s.ownershipError()
		}
		return errf(CodeInternal, "delete failed", err)
	}
	s.audit(ctx, uid, meta, "player_delete_self", "plugin_player_delete", map[string]any{
		"pid": pid, "name": name, "uuid": uuid,
	})
	if s.EmitEvent != nil {
		s.EmitEvent(ctx, "player.deleted", plugin.EventPayload{
			UserID: uid, PlayerID: pid, PlayerName: name, UUID: uuid, IP: meta.IP,
		})
	}
	return nil
}

func (s *Service) SetTexture(ctx context.Context, uid, pid int64, kind string, tid int64, meta CallMeta) (PlayerView, *Error) {
	if e := s.ensure(); e != nil {
		return PlayerView{}, e
	}
	if kind != "skin" && kind != "cape" {
		return PlayerView{}, errf(CodeTypeMismatch, "texture type must be skin or cape", nil)
	}
	if _, e := s.GetPlayer(ctx, uid, pid); e != nil {
		return PlayerView{}, e
	}
	if tid != 0 {
		// The closet IS the wearable set — one concept, as in blessing. Own
		// uploads no longer wear on bare ownership: they get collected at upload
		// (textures.autoCloset) and history was backfilled by migration 0022, so
		// the only way an own upload is missing here is that its owner removed
		// it, which is exactly the state that should refuse the wear.
		inCloset, cerr := s.Store.InCloset(ctx, uid, tid)
		if cerr != nil {
			return PlayerView{}, errf(CodeInternal, "could not check the closet", cerr)
		}
		if !inCloset {
			return PlayerView{}, errf(CodeNotOwned, "this texture is not in your closet", nil)
		}
		typ, err := s.Store.TextureType(ctx, tid)
		if err != nil || typ != kind {
			return PlayerView{}, errf(CodeTypeMismatch, "texture type does not match this operation", err)
		}
	}
	skinTID, capeTID, err := s.Store.PlayerTextureIDs(ctx, pid)
	if err != nil {
		return PlayerView{}, errf(CodeInternal, "could not read the player", err)
	}
	if kind == "skin" {
		skinTID = tid
	} else {
		capeTID = tid
	}
	if err := s.Store.SetPlayerTextures(ctx, pid, skinTID, capeTID); err != nil {
		return PlayerView{}, errf(CodeInternal, "could not update the appearance", err)
	}
	s.audit(ctx, uid, meta, "", "plugin_player_texture", map[string]any{
		"pid": pid, "kind": kind, "textureId": tid,
	})
	v, e := s.PlayerView(ctx, uid, pid)
	return v, e
}

func (s *Service) Account(ctx context.Context, uid int64) (AccountView, *Error) {
	if e := s.ensure(); e != nil {
		return AccountView{}, e
	}
	u, err := s.Store.UserByID(ctx, uid)
	if err != nil {
		return AccountView{}, errf(CodeInternal, "could not read the account", err)
	}
	perm, _ := s.Store.UserPermission(ctx, uid)
	players, _ := s.Store.PlayersByUID(ctx, uid)
	avatarHash, _ := s.Store.UserAvatarHash(ctx, uid)
	avatar := s.avatarURL(avatarHash)
	if avatar == "" {
		avatar = siteurl.AvatarUser(uid)
	}
	return AccountView{
		ID: u.ID, Nickname: u.Nickname, Permission: perm,
		PlayerLimit: s.playerLimit(ctx, uid), PlayerCount: len(players),
		AvatarURL: avatar, StorageBytes: s.storageBytes(ctx, uid),
	}, nil
}

func (s *Service) Limits(ctx context.Context) SiteLimits {
	maxKB := s.Store.MaxUploadKB(ctx, 1024)
	return SiteLimits{
		MaxUploadBytes: int64(maxKB) << 10,
		MaxPlayers:     s.MaxChars,
		OIDCEnabled:    s.OIDCEnabled,
	}
}

func (s *Service) storageBytes(ctx context.Context, uid int64) int64 {
	rows, err := s.Store.ListTextures(ctx, uid)
	if err != nil {
		return 0
	}
	var total int64
	for _, tx := range rows {
		if s.Textures == nil {
			break
		}
		if p, err := s.Textures.Path(tx.Hash); err == nil {
			if fi, err := os.Stat(p); err == nil {
				total += fi.Size()
			}
		}
	}
	return total
}
