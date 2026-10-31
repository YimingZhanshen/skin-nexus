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
	"image"
	"image/png"
	"log"
	"net/http"
	"strconv"

	"skin-nexus/server/internal/avatargen"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/texture"
)

const skinSessionCookie = "skin_session"

type avatarDeps struct {
	st       *store.Mysql
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

// privatePreviewAllowed mirrors the skinlib detail page visibility rule:
// the owner or an admin (permission > 0) may see a private texture.
func privatePreviewAllowed(st previewAuthStore, r *http.Request, tid int64) bool {
	c, err := r.Cookie(skinSessionCookie)
	if err != nil {
		return false
	}
	uid, err := st.SessionUser(r.Context(), c.Value)
	if err != nil || uid == 0 {
		return false
	}
	if perm, err := st.UserPermission(r.Context(), uid); err == nil && perm > 0 {
		return true
	}
	owner, err := st.TextureOwner(r.Context(), tid)
	return err == nil && owner == uid
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

// serveAvatar renders hash as an avatar and writes it; nil hash / missing
// file / undecodable PNG falls back to the embedded default avatar
// (legacy: null texture or $disk->missing → default image, 200).
func (d *avatarDeps) serveAvatar(w http.ResponseWriter, r *http.Request, hash, typ string, threeD bool) {
	if typ != "" && typ != "skin" {
		writeAvatarErr(w, http.StatusUnprocessableEntity, "texture is not a skin")
		return
	}
	size := avatarSize(r)
	// Plugin avatar delegation: only for a real texture hash (never the
	// default avatar) and only when the plugin's manifest allowlists the host.
	if hash != "" && d.redirect != nil {
		if target, ok := d.redirect(r.Context(), hash, size, threeD); ok {
			w.Header().Set("Cache-Control", "public, max-age=60")
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
	writePNG(w, img, "public, max-age=86400")
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
	d.serveAvatar(w, r, skinHash, "skin", r.URL.Query().Has("3d"))
}

func (d *avatarDeps) handleAvatarUser(w http.ResponseWriter, r *http.Request) {
	uid, perr := strconv.ParseInt(r.PathValue("uid"), 10, 64)
	if perr != nil {
		// Legacy: User::find('abc') → null → default avatar.
		d.serveAvatar(w, r, "", "", r.URL.Query().Has("3d"))
		return
	}
	tid, err := d.st.UserAvatarTID(r.Context(), uid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			d.serveAvatar(w, r, "", "", r.URL.Query().Has("3d")) // user missing → default
			return
		}
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	if tid == 0 {
		d.serveAvatar(w, r, "", "", r.URL.Query().Has("3d")) // no avatar set → default
		return
	}
	d.serveAvatarTID(w, r, tid)
}

func (d *avatarDeps) handleAvatarTID(w http.ResponseWriter, r *http.Request) {
	tid, perr := strconv.ParseInt(r.PathValue("tid"), 10, 64)
	if perr != nil || tid <= 0 {
		d.serveAvatar(w, r, "", "", r.URL.Query().Has("3d"))
		return
	}
	d.serveAvatarTID(w, r, tid)
}

func (d *avatarDeps) serveAvatarTID(w http.ResponseWriter, r *http.Request, tid int64) {
	hash, typ, _, err := d.st.TextureByTID(r.Context(), tid)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			d.serveAvatar(w, r, "", "", r.URL.Query().Has("3d")) // texture missing → default
			return
		}
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	d.serveAvatar(w, r, hash, typ, r.URL.Query().Has("3d"))
}

func (d *avatarDeps) handleAvatarHash(w http.ResponseWriter, r *http.Request) {
	hash := r.PathValue("hash")
	typ, _, err := d.st.TextureMetaByHash(r.Context(), hash)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			d.serveAvatar(w, r, "", "", r.URL.Query().Has("3d"))
			return
		}
		http.Error(w, "db error", http.StatusInternalServerError)
		return
	}
	d.serveAvatar(w, r, hash, typ, r.URL.Query().Has("3d"))
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
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		http.ServeContent(w, r, "", zeroTime(), f)
		return
	}
	skin, lerr := avatargen.LoadSkinReader(f)
	if lerr != nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	writePNG(w, avatargen.NormalizeBackground(skin), "public, max-age=31536000, immutable")
}

func writePNG(w http.ResponseWriter, img image.Image, cache string) {
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", cache)
	if err := png.Encode(w, img); err != nil {
		log.Printf("avatar: encode: %v", err)
	}
}
