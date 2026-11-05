package main

// /texture-render/{hash} was closed in 65e2beb because a content address is not
// a capability: the panel, the library list and the detail page hand the URL of
// a private sheet to its owner, and any observer who once saw the URL could
// fetch it — with `public, max-age=86400` on the response, which is exactly what
// a CDN in front of the site needs to turn one player's private face into a
// cached asset for everyone. /avatar/hash/{hash} is the sibling that kept the
// old assumption, so it gets the same rule: the session that may see the texture
// sees the face, anybody else gets the default avatar and nothing cacheable.

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func avatarHashRequest(hash, cookie string) *http.Request {
	r := requestAs("/avatar/hash/"+hash, cookie)
	r.SetPathValue("hash", hash)
	return r
}

func TestAvatarHashHonoursVisibility(t *testing.T) {
	const priv = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const alsoPriv = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	const pub = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	st := fakeAvatarStore{
		fakePreviewAuthStore: fakePreviewAuthStore{sessionUID: 7, owner: 7},
		metaByHash:           map[string]string{priv: "skin", alsoPriv: "skin", pub: "skin"},
		visibility: map[string]fakeVisibility{
			priv:     {owners: []int64{7}},
			alsoPriv: {owners: []int64{42}},
			pub:      {public: true},
		},
	}
	d := newAvatarDeps(t, st)
	putSkin(t, d, priv)

	t.Run("the owner's session gets the real face, uncacheable", func(t *testing.T) {
		rec := httptest.NewRecorder()
		d.handleAvatarHash(rec, avatarHashRequest(priv, "s"))
		if rec.Code != http.StatusOK {
			t.Fatalf("owner private hash = %d, want 200", rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
			t.Fatalf("private avatar cache-control = %q, want it out of shared caches", cc)
		}
	})

	t.Run("an anonymous visitor gets the default face, not somebody's sheet", func(t *testing.T) {
		rec := httptest.NewRecorder()
		d.handleAvatarHash(rec, avatarHashRequest(priv, ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("anonymous private hash = %d, want the default avatar (200)", rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
			t.Fatalf("anonymous private hash cache-control = %q, want no-store", cc)
		}
		owner := httptest.NewRecorder()
		d.handleAvatarHash(owner, avatarHashRequest(priv, "s"))
		if bytes.Equal(owner.Body.Bytes(), rec.Body.Bytes()) {
			t.Fatal("the anonymous answer is byte-identical to the owner's: the private face was served")
		}
	})

	t.Run("a stranger with a session gets the default face too", func(t *testing.T) {
		st2 := st
		st2.fakePreviewAuthStore = fakePreviewAuthStore{sessionUID: 99, owner: 7}
		rec := httptest.NewRecorder()
		newAvatarDeps(t, st2).handleAvatarHash(rec, avatarHashRequest(priv, "s"))
		if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
			t.Fatalf("stranger session private hash cache-control = %q, want no-store", cc)
		}
	})

	t.Run("staff may see it", func(t *testing.T) {
		st2 := st
		st2.fakePreviewAuthStore = fakePreviewAuthStore{sessionUID: 99, permission: 2, owner: 7}
		rec := httptest.NewRecorder()
		newAvatarDeps(t, st2).handleAvatarHash(rec, avatarHashRequest(alsoPriv, "s"))
		if rec.Code != http.StatusOK {
			t.Fatalf("staff private hash = %d, want 200", rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
			t.Fatalf("staff private hash cache-control = %q, want no-store", cc)
		}
	})

	t.Run("a public hash keeps its cacheable behaviour", func(t *testing.T) {
		rec := httptest.NewRecorder()
		d.handleAvatarHash(rec, avatarHashRequest(pub, ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("public hash = %d, want 200", rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=86400" {
			t.Fatalf("public avatar cache-control = %q, want it cacheable", cc)
		}
	})

	t.Run("an unknown hash still falls back to the default face", func(t *testing.T) {
		rec := httptest.NewRecorder()
		d.handleAvatarHash(rec, avatarHashRequest("dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd", ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("unknown hash = %d, want the legacy default (200)", rec.Code)
		}
	})
}
