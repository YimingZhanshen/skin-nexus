package account

import (
	"context"
	"errors"
	"log"
	"skin-nexus/server/internal/i18n"
	"skin-nexus/server/internal/plugin"
	"skin-nexus/server/internal/store"
)

// DeleteTexture removes one texture row and, once no other row shares its
// content address, the file behind it. ownerUID scopes the row to its owner;
// pass 0 for an administrator, who may delete any row.
//
// The content lock spans the database write and the unlink: an upload racing in
// between would otherwise insert a row for bytes that were just deleted.
func (s *Service) DeleteTexture(ctx context.Context, actorUID, tid, ownerUID int64, force bool, reason string, meta CallMeta) *Error {
	if e := s.ensure(); e != nil {
		return e
	}
	hash, typ, err := s.Store.TextureContentRef(ctx, tid)
	if errors.Is(err, store.ErrNotFound) {
		return errf(i18n.MsgTextureNotFound, "texture not found", err)
	}
	if err != nil {
		return errf(CodeInternal, "could not read the texture", err)
	}

	if ge := s.guard(ctx, "texture.delete.check", plugin.EventPayload{
		UserID: actorUID, Type: typ, Hash: hash, IP: meta.IP,
		Data: map[string]any{"tid": tid, "force": force, "reason": reason},
	}); ge != nil {
		return ge
	}

	var res store.DeleteResult
	lockErr := s.Store.WithHashLock(ctx, hash, func(ctx context.Context) error {
		var err error
		res, err = s.Store.DeleteTexture(ctx, tid, ownerUID, actorUID, force, reason)
		if err != nil {
			return err
		}
		// The row is already committed as gone, so a failed unlink leaves a file
		// nothing refers to — recoverable, and better than reporting that the
		// deletion failed when it did not.
		if res.ShareLeft == 0 {
			if err := s.Textures.Remove(res.Hash); err != nil {
				log.Printf("texture file remove failed (hash %s): %v", res.Hash, err)
			}
		}
		return nil
	})
	switch {
	case errors.Is(lockErr, store.ErrNotFound):
		return errf(i18n.MsgTextureNotFound, "texture not found", lockErr)
	case errors.Is(lockErr, store.ErrForbidden):
		return errf(CodeForbidden, "only the uploader may delete this texture", lockErr)
	case errors.Is(lockErr, store.ErrTextureInUse):
		// Naming the blockers is the point: "in use" without a who is
		// something the site owner cannot act on.
		return errDetails(CodeInUse, "the texture is still in use", map[string]any{
			"players":     res.Refs.Players,
			"playerCount": len(res.Refs.Players),
			"closets":     res.Refs.Closets,
			"avatars":     res.Refs.Avatars,
		})
	case errors.Is(lockErr, store.ErrBusy):
		return errf(CodeBusy, "this texture is locked by another operation, try again shortly", lockErr)
	case lockErr != nil:
		log.Printf("texture delete failed: %v", lockErr)
		return errf(CodeInternal, "could not delete the texture", lockErr)
	}

	if s.EmitEvent != nil {
		s.EmitEvent(ctx, "texture.deleted", plugin.EventPayload{
			UserID: actorUID, Hash: res.Hash, Type: res.Type, Data: map[string]any{"tid": tid},
		})
	}
	s.audit(ctx, actorUID, meta, "texture_delete", "plugin_texture_delete", map[string]any{
		"tid": tid, "hash": res.Hash, "type": res.Type,
		"fileRemoved": res.ShareLeft == 0, "reason": reason,
	})
	return nil
}
