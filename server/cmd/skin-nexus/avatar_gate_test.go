package main

// A texture addressed by tid is enumerable — tids are sequential — so the
// avatar route must honour the same visibility rule the preview route already
// does. Identity routes (by player name / user id) serve whatever the account
// chose as its public face and are deliberately ungated; these tests pin both
// halves of that boundary so a future change cannot quietly widen it.
import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/texture"
	"skin-nexus/server/internal/ygg"
)

type fakeAvatarStore struct {
	fakePreviewAuthStore
	publicByTID  map[int64]bool
	byTID        map[int64]string
	publicByHash map[string]bool
	metaByHash   map[string]string
	avatarByUID  map[int64]int64
	playerByName map[string]ygg.Player
	playerByPID  map[int64]ygg.Player
}

func (f fakeAvatarStore) TextureIsPublic(_ context.Context, tid int64) (bool, error) {
	return f.publicByTID[tid], nil
}

func (f fakeAvatarStore) TextureIsPublicByHash(_ context.Context, hash string) (bool, error) {
	return f.publicByHash[hash], nil
}

func (f fakeAvatarStore) TextureByTID(_ context.Context, tid int64) (string, string, bool, error) {
	hash, ok := f.byTID[tid]
	if !ok {
		return "", "", false, store.ErrNotFound
	}
	return hash, "skin", false, nil
}

func (f fakeAvatarStore) TextureMetaByHash(_ context.Context, hash string) (string, bool, error) {
	typ, ok := f.metaByHash[hash]
	if !ok {
		return "", false, store.ErrNotFound
	}
	return typ, false, nil
}

func (f fakeAvatarStore) UserAvatarTID(_ context.Context, uid int64) (int64, error) {
	tid, ok := f.avatarByUID[uid]
	if !ok {
		return 0, store.ErrNotFound
	}
	return tid, nil
}

func (f fakeAvatarStore) PlayerByName(_ context.Context, name string) (ygg.Player, error) {
	p, ok := f.playerByName[name]
	if !ok {
		return ygg.Player{}, store.ErrNotFound
	}
	return p, nil
}

func (f fakeAvatarStore) PlayerTextureHashes(_ context.Context, pid int64) (string, string, string, error) {
	p, ok := f.playerByPID[pid]
	if !ok {
		return "", "", "", store.ErrNotFound
	}
	return p.SkinHash, "default", "", nil
}

func (f fakeAvatarStore) NewestPublicSkin(context.Context) (string, int64, bool, error) {
	return "", 0, false, store.ErrNotFound
}

func newAvatarDeps(t *testing.T, st avatarStore) *avatarDeps {
	t.Helper()
	ts, err := texture.NewStore(t.TempDir())
	if err != nil {
		t.Fatalf("texture store: %v", err)
	}
	return &avatarDeps{st: st, ts: ts}
}

func requestAs(path, cookie string) *http.Request {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	if cookie != "" {
		r.AddCookie(&http.Cookie{Name: skinSessionCookie, Value: cookie})
	}
	return r
}

// PathValue only resolves inside a routed request, so a bare httptest request
// has to declare its own segment — otherwise every handler below silently sees
// an empty tid and falls back to the default avatar.
func tidRequest(tid, cookie string) *http.Request {
	r := requestAs("/avatar/"+tid, cookie)
	r.SetPathValue("tid", tid)
	return r
}

// The hole: /avatar/{tid} handed out private renders to anyone.
func TestAvatarTIDHonoursVisibility(t *testing.T) {
	st := fakeAvatarStore{
		fakePreviewAuthStore: fakePreviewAuthStore{sessionUID: 7, owner: 7},
		publicByTID:          map[int64]bool{1: true, 2: false},
		byTID:                map[int64]string{1: "aa", 2: "bb"},
	}
	d := newAvatarDeps(t, st)

	t.Run("anonymous may not render a private tid", func(t *testing.T) {
		rec := httptest.NewRecorder()
		d.handleAvatarTID(rec, tidRequest("2", ""))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("anonymous private tid = %d, want 404", rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "" {
			t.Fatalf("a 404 must not carry a cache directive, got %q", cc)
		}
	})

	t.Run("another logged-in user may not either", func(t *testing.T) {
		st2 := st
		st2.fakePreviewAuthStore = fakePreviewAuthStore{sessionUID: 99, owner: 7}
		rec := httptest.NewRecorder()
		newAvatarDeps(t, st2).handleAvatarTID(rec, tidRequest("2", "s"))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("stranger session private tid = %d, want 404", rec.Code)
		}
	})

	t.Run("the owner may", func(t *testing.T) {
		rec := httptest.NewRecorder()
		d.handleAvatarTID(rec, tidRequest("2", "s"))
		if rec.Code != http.StatusOK {
			t.Fatalf("owner private tid = %d, want 200", rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "private, no-store" {
			t.Fatalf("private avatar cache-control = %q, want %q", cc, "private, no-store")
		}
		if rec.Header().Get("Vary") != "Cookie" {
			t.Fatal("a session-dependent render must vary on Cookie")
		}
	})

	t.Run("an admin may", func(t *testing.T) {
		st2 := st
		st2.fakePreviewAuthStore = fakePreviewAuthStore{sessionUID: 8, permission: 2, owner: 7}
		rec := httptest.NewRecorder()
		newAvatarDeps(t, st2).handleAvatarTID(rec, tidRequest("2", "s"))
		if rec.Code != http.StatusOK {
			t.Fatalf("admin private tid = %d, want 200", rec.Code)
		}
	})

	t.Run("a public tid stays anonymously cacheable", func(t *testing.T) {
		rec := httptest.NewRecorder()
		d.handleAvatarTID(rec, tidRequest("1", ""))
		if rec.Code != http.StatusOK {
			t.Fatalf("public tid = %d, want 200", rec.Code)
		}
		if cc := rec.Header().Get("Cache-Control"); cc != "public, max-age=86400" {
			t.Fatalf("public avatar cache-control = %q", cc)
		}
	})
}

// The boundary that must NOT move: identity avatars are a public surface by
// design (launcher profile UIs, union member lists, WearTargetPicker).
func TestAvatarIdentityRoutesStayUngated(t *testing.T) {
	st := fakeAvatarStore{
		fakePreviewAuthStore: fakePreviewAuthStore{sessionErr: store.ErrNotFound},
		publicByTID:          map[int64]bool{2: false},
		byTID:                map[int64]string{2: "bb"},
		avatarByUID:          map[int64]int64{7: 2},
	}
	d := newAvatarDeps(t, st)

	rec := httptest.NewRecorder()
	userReq := requestAs("/avatar/user/7", "")
	userReq.SetPathValue("uid", "7")
	d.handleAvatarUser(rec, userReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("identity avatar by uid = %d, want 200 — this route is public on purpose", rec.Code)
	}
}
