package adminapi

// Inbox emission points (2026-11-02). Moderation and ban decisions previously
// reached the player only by them noticing the page had changed.

import (
	"context"
	"log"
)

// notify appends one inbox row. Best-effort by design: losing a notification
// must not fail the operation it reports, and the audit log stays the
// authoritative record of what happened.
func (b *Backend) notify(ctx context.Context, uid int64, kind string, tid int64, ref string) {
	if uid <= 0 {
		return
	}
	if err := b.Store.InsertNotification(ctx, uid, kind, tid, ref); err != nil {
		log.Printf("notify %s uid=%d: %v", kind, uid, err)
	}
}

func (b *Backend) notifyUser(ctx context.Context, uid int64, kind string) {
	b.notify(ctx, uid, kind, 0, "")
}

// notifyTextureOwner reaches the uploader. A texture deleted between the
// decision and here simply has nobody to notify.
func (b *Backend) notifyTextureOwner(ctx context.Context, kind string, tid int64) {
	owner, err := b.Store.TextureOwner(ctx, tid)
	if err != nil {
		return
	}
	b.notify(ctx, owner, kind, tid, b.Store.TextureNameForNotification(ctx, tid))
}
