package main

// Avatar & preview HTTP surface — blessing TextureController parity
// (routes/static.php + manual man/api/avatars-and-previews.md, semantics
// verified against the live legacy site per the 取证实测优先 iron rule):
//
//	GET /avatar/player/{name}  player missing → 404; no skin/file → default
//	GET /avatar/user/{uid}     user or avatar missing → default (200)
//	GET /avatar/{tid}          texture missing → default (200)
//	GET /avatar/hash/{hash}    texture missing → default (200)
//	any avatar: cape-typed texture → 422 (TextureController.php:141)
//	GET /preview/{tid}         texture or file missing → 404
//	GET /preview/hash/{hash}   same
//
// Query params: size (default 100, clamped 8..1024 — legacy had no clamp
// and a huge resize was a memory DoS), `3d` (presence → 45° cube view),
// `png` (accepted as a no-op: output is always PNG, which the legacy
// contract explicitly allows — clients must sniff Content-Type).

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"log"
	"net/http"
	"strconv"

	"skin-nexus/server/internal/avatargen"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/texture"
	"skin-nexus/server/internal/ygg"
)

const skinSessionCookie = "skin_session"

// avatarStore is the read slice of the store the avatar/preview surface needs.
// Naming it keeps the handlers testable: the visibility rule below is a
// security property, and a security property that can only be exercised
// against a live database tends to get dropped.
type avatarStore interface {
	previewAuthStore
	PlayerByName(ctx context.Context, name string) (ygg.Player, error)
	PlayerTextureHashes(ctx context.Context, pid int64) (skinHash, skinModel, capeHash string, err error)
	UserAvatarTID(ctx context.Context, uid int64) (int64, error)
	TextureByTID(ctx context.Context, tid int64) (hash, typ string, slim bool, err error)
	TextureMetaByHash(ctx context.Context, hash string) (typ string, slim bool, err error)
	TextureIsPublic(ctx context.Context, tid int64) (bool, error)
	TextureIsPublicByHash(ctx context.Context, hash string) (bool, error)
	TextureHashVisibility(ctx context.Context, hash string) (public bool, privateOwners []int64, err error)
	NewestPublicSkin(ctx context.Context) (hash string, tid int64, slim bool, err error)
}

type avatarDeps struct {
	st       avatarStore
	ts       *texture.Store
	redirect func(ctx context.Context, hash string, size int, threeD bool) (string, bool)
}

// previewAuthStore is the slice of the store needed to authorize a private
// preview: session → uid, uid → permission, tid → owner.
type previewAuthStore interface {
	SessionUser(ctx context.Context, token string) (int64, error)
	UserPermission(ctx context.Context, uid int64) (int, error)
	TextureOwner(ctx context.Context, tid int64) (int64, error)
}

// sessionViewer resolves the request cookie to a viewer: who, and whether they
// run the site. Both private-viewing checks below start here so the staff rule
// exists in exactly one place.
func sessionViewer(st previewAuthStore, r *http.Request) (int64, bool) {
	c, err := r.Cookie(skinSessionCookie)
	if err != nil {
		return 0, false
	}
	uid, err := st.SessionUser(r.Context(), c.Value)
	if err != nil || uid == 0 {
		return 0, false
	}
	if perm, err := st.UserPermission(r.Context(), uid); err == nil && perm > 0 {
		return uid, true
	}
	return uid, false
}

// privatePreviewAllowed mirrors the skinlib detail page visibility rule:
// the owner or an admin (permission > 0) may see a private texture.
func privatePreviewAllowed(st previewAuthStore, r *http.Request, tid int64) bool {
	uid, staff := sessionViewer(st, r)
	if staff {
		return true
	}
	if uid == 0 {
		return false
	}
	owner, err := st.TextureOwner(r.Context(), tid)
	return err == nil && owner == uid
}

// privateHashAllowed is the content-addressed twin of the rule above. The
// address itself is not listed, so only staff or one of the accounts holding a
// private row for those bytes may look — and "the owner" is a set, because two
// uploaders can each claim the same bytes without either seeing the other.
func privateHashAllowed(st previewAuthStore, r *http.Request, privateOwners []int64) bool {
	uid, staff := sessionViewer(st, r)
	if staff {
		return true
	}
	if uid == 0 {
		return false
	}
	for _, o := range privateOwners {
		if o == uid {
			return true
		}
	}
	return false
}

// previewCacheControl keeps private renders out of shared caches. Public
// previews stay immutable; the URL carries avatargen.RenderVersion.
func previewCacheControl(private bool) string {
	if private {
		return "private, no-store"
	}
	return "public, max-age=31536000, immutable"
}

func avatarSize(r *http.Request) int {
	const def = 100
	s := r.URL.Query().Get("size")
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 8 {
		return def
	}
	if n > 1024 {
		return 1024
	}
	return n
}

func writeAvatarErr(w http.ResponseWriter, status int, desc string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{
		"error": "unprocessable_entity", "error_description": desc,
	})
}

// avatarCacheControl keeps a session-authorized private render out of shared
// caches; the anonymous default avatar and public textures stay cacheable.
func avatarCacheControl(private bool) string {
	if private {
		return "private, no-store"
	}
	return "public, max-age=86400"
}

// serveAvatar renders hash as an avatar and writes it; nil hash / missing
// file / undecodable PNG falls back to the embedded default avatar
// (legacy: null texture or $disk->missing → default image, 200).
func (d *avatarDeps) serveAvatar(w http.ResponseWriter, r *http.Request, hash, typ string, threeD, private bool) {
	if typ != "" && typ != "skin" {
		writeAvatarErr(w, http.StatusUnprocessableEntity, "texture is not a skin")
		return
	}
	size := avatarSize(r)
	if private {
		w.Header().Set("Vary", "Cookie")
		w.Header().Set("X-Robots-Tag", "noindex")
	}
	// Plugin avatar delegation: only for a real texture hash (never the
	// default avatar) and only when the plugin's manifest allowlists the host.
	if hash != "" && d.redirect != nil {
		if target, ok := d.redirect(r.Context(), hash, size, threeD); ok {
			if private {
				w.Header().Set("Cache-Control", "private, no-store")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=60")
			}
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
	}
	var img *image.NRGBA
	if hash != "" {
		if f, err := d.ts.Open(hash); err == nil {
			skin, lerr := avatargen.LoadSkinReader(f)
			f.Close()
			if lerr == nil {
				img = avatargen.RenderAvatar(skin, size, threeD)
			}
		}
	}
	if img == nil {
		def, err := avatargen.DefaultAvatar(size, threeD)
		if err != nil {
			log.Printf("avatar: default asset: %v", err)
			http.Error(w, "default avatar missing", http.StatusInternalServerError)
			return
		}
		img = def
	}
	writePNG(w, img, avatarCacheControl(private))
}

func (d *avatarDeps) handleAvatarPlayer(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	p, err := d.st.PlayerByName(r.Context(), name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		log.Printf("avatar/player %q: %v", name, err)
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	skinHash, _, _, err := d.st.PlayerTextureHashes(r.Context(), p.ID)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	d.serveAvatar(w, r, skinHash, "skin", r.URL.Query().Has("3d"), false)
}

// handleAvatarUser serves the face an account chose to present. Identity routes
// (by player name, by user id) are public on purpose — launcher profile UIs,
// union member lists and the in-app wearer picker all render them — because
// `textures.public` says "listed in the skin library", not "this face is
// secret". Only the routes that address a texture directly are gated.
func (d *avatarDeps) handleAvatarUser(w http.ResponseWriter, r *http.Request) {
	uid, perr := strconv.ParseInt(r.PathValue("uid"), 10, 64)
	if perr != nil {
		// Legacy: User::find('abc') → null → default avatar.
		d.serveAvatar(w, r, "", "", r.URL.Query().Has("3d"), false)
		return
	}
	tid, err := d.st.UserAvatarTID(r.Context(), uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			d.serveAvatar(w, r, "", "", r.URL.Query().Has("3d"), false) // user missing → default
			return
		}
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if tid == 0 {
		d.serveAvatar(w, r, "", "", r.URL.Query().Has("3d"), false) // no avatar set → default
		return
	}
	d.serveAvatarTID(w, r, tid, false)
}

func (d *avatarDeps) handleAvatarTID(w http.ResponseWriter, r *http.Request) {
	tid, perr := strconv.ParseInt(r.PathValue("tid"), 10, 64)
	if perr != nil || tid <= 0 {
		d.serveAvatar(w, r, "", "", r.URL.Query().Has("3d"), false)
		return
	}
	// A tid is sequential, so this route is enumerable: it must honour the same
	// visibility rule /preview/{tid} applies, or a private skin's face leaks to
	// anyone who walks the integers.
	pub, err := d.st.TextureIsPublic(r.Context(), tid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			d.serveAvatar(w, r, "", "", r.URL.Query().Has("3d"), false) // texture missing → default
			return
		}
		log.Printf("avatar/%d visibility: %v", tid, err)
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if !pub && !privatePreviewAllowed(d.st, r, tid) {
		http.NotFound(w, r)
		return
	}
	d.serveAvatarTID(w, r, tid, !pub)
}

func (d *avatarDeps) serveAvatarTID(w http.ResponseWriter, r *http.Request, tid int64, private bool) {
	hash, typ, _, err := d.st.TextureByTID(r.Context(), tid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			d.serveAvatar(w, r, "", "", r.URL.Query().Has("3d"), private) // texture missing → default
			return
		}
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	d.serveAvatar(w, r, hash, typ, r.URL.Query().Has("3d"), private)
}

// handleAvatarHash addresses a texture by its content address, which is only
// ever handed to people who may see it (the raw /textures/{hash} route runs the
// same capability model), so no visibility lookup happens here.
func (d *avatarDeps) handleAvatarHash(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	typ, _, err := d.st.TextureMetaByHash(r.Context(), hash)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			d.serveAvatar(w, r, "", "", r.URL.Query().Has("3d"), false)
			return
		}
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	d.serveAvatar(w, r, hash, typ, r.URL.Query().Has("3d"), false)
}

// preview renders the full-body front+back views. Unlike avatars there is
// no default fallback: legacy aborts 404 when the texture row or the
// backing file is missing (probe + controller agree).
func (d *avatarDeps) handlePreviewTID(w http.ResponseWriter, r *http.Request) {
	tid, perr := strconv.ParseInt(r.PathValue("tid"), 10, 64)
	if perr != nil || tid <= 0 {
		http.NotFound(w, r)
		return
	}
	pub, err := d.st.TextureIsPublic(r.Context(), tid)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	private := !pub
	if private && !privatePreviewAllowed(d.st, r, tid) {
		http.NotFound(w, r)
		return
	}
	hash, typ, slim, err := d.st.TextureByTID(r.Context(), tid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	d.servePreview(w, r, hash, typ, slim, private)
}

func (d *avatarDeps) handlePreviewHash(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	pub, perr := d.st.TextureIsPublicByHash(r.Context(), hash)
	if perr != nil || !pub {
		http.NotFound(w, r)
		return
	}
	typ, slim, err := d.st.TextureMetaByHash(r.Context(), hash)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	d.servePreview(w, r, hash, typ, slim, false)
}

func (d *avatarDeps) servePreview(w http.ResponseWriter, r *http.Request, hash, typ string, slim, private bool) {
	if typ != "skin" {
		// A cape through the body renderer is garbage — 404 keeps the
		// surface honest (legacy renders it anyway; deviation noted).
		http.NotFound(w, r)
		return
	}
	f, err := d.ts.Open(hash)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	skin, lerr := avatargen.LoadSkinReader(f)
	if lerr != nil {
		http.NotFound(w, r)
		return
	}
	ratio := 10.0
	if r.URL.Query().Get("small") == "1" {
		ratio = 5
	}
	if private {
		w.Header().Set("Vary", "Cookie")
		w.Header().Set("X-Robots-Tag", "noindex")
	}
	writePNG(w, avatargen.RenderPreviewAt(skin, slim, ratio), previewCacheControl(private))
}

// handleViewerTexture serves the background-normalized sheet used by the
// detail-page skinview3d viewer. The raw /textures/{hash} file is returned
// for downloads and plugin URL filters; this variant applies the legacy
// white-overlay rule so no-alpha skins (white helmet layers) look the same
// in WebGL as in the server-rendered preview.
func (d *avatarDeps) handleViewerTexture(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	typ, _, err := d.st.TextureMetaByHash(r.Context(), hash)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	// /skinlib/{tid} shows this URL for private textures too, so the route is
	// reachable by whoever the detail page let in — and it used to answer them
	// all with `public, immutable`, which is exactly the directive a CDN needs
	// to turn one owner's private sheet into a cached site-wide asset.
	public, privateOwners, verr := d.st.TextureHashVisibility(r.Context(), hash)
	if verr != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	private := !public
	if private && !privateHashAllowed(d.st, r, privateOwners) {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	cache := previewCacheControl(private)
	if private {
		w.Header().Set("Vary", "Cookie")
		w.Header().Set("X-Robots-Tag", "noindex")
	}
	f, err := d.ts.Open(hash)
	if err != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	defer f.Close()
	// The background-keyed UV map only applies to skins; capes keep their
	// raw bytes even if someone requests this route directly.
	if typ != "skin" {
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", cache)
		http.ServeContent(w, r, "", zeroTime(), f)
		return
	}
	skin, lerr := avatargen.LoadSkinReader(f)
	if lerr != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	writePNG(w, avatargen.NormalizeBackground(skin), cache)
}

func writePNG(w http.ResponseWriter, img image.Image, cache string) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", cache)
	if err := png.Encode(w, img); err != nil {
		log.Printf("avatar: encode: %v", err)
	}
}

// smokePreview runs the preview pipeline on real data: the newest public skin,
// opened through the file layer and rendered exactly the way /preview renders
// it. The admin self-check calls this because the failure that bites operators
// is never "the renderer is missing from the binary" — it is "the texture row
// exists and its file does not" (a data volume that moved, an import that
// skipped files). The bool reports whether anything was rendered at all: an
// empty library is not a fault, but it is not a passed test either.
func (d *avatarDeps) smokePreview(ctx context.Context) (bool, error) {
	hash, tid, slim, err := d.st.NewestPublicSkin(ctx)
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	f, err := d.ts.Open(hash)
	if err != nil {
		return true, fmt.Errorf("texture %d: %w", tid, err)
	}
	defer f.Close()
	skin, err := avatargen.LoadSkinReader(f)
	if err != nil {
		return true, fmt.Errorf("texture %d: %w", tid, err)
	}
	if img := avatargen.RenderPreviewAt(skin, slim, 5); img == nil || img.Bounds().Dx() < 64 {
		return true, fmt.Errorf("texture %d: the renderer produced an empty preview", tid)
	}
	return true, nil
}
