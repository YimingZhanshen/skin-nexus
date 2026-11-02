package account

import (
	"bytes"
	"context"
	"errors"
	"log"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"skin-nexus/server/internal/siteurl"
	"time"
	"unicode/utf8"

	"skin-nexus/server/internal/plugin"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/texture"
)

func validSourceURL(raw string) bool {
	if len(raw) > 512 {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return false
	}
	return u.User == nil
}

func (s *Service) textureView(ctx context.Context, id int64, typ, hash, model, desc,
	detail, source, sourceURL string, at time.Time) TextureView {
	v := TextureView{
		ID: id, Type: typ, Hash: hash, Model: model, Description: desc,
		DetailMD: detail, Source: source, SourceURL: sourceURL,
		URL: s.textureURL(hash), PreviewURL: s.previewURL(id, typ),
	}
	if typ == "skin" {
		v.AvatarURL = s.avatarURL(hash)
	}
	if !at.IsZero() {
		v.UploadedAt = at.UTC().Format(time.RFC3339)
	}
	if s.Textures != nil {
		if p, err := s.Textures.Path(hash); err == nil {
			if fi, err := os.Stat(p); err == nil {
				v.Bytes = fi.Size()
			}
		}
	}
	return v
}

func (s *Service) textureRowView(ctx context.Context, row store.TextureRow) TextureView {
	return s.textureView(ctx, row.ID, row.Type, row.Hash, row.Model, row.Description,
		row.DetailMD, row.Source, row.SourceURL, row.UploadAt)
}

func (s *Service) ListTextures(ctx context.Context, uid int64) ([]TextureView, *Error) {
	if e := s.ensure(); e != nil {
		return nil, e
	}
	rows, err := s.Store.ListTextures(ctx, uid)
	if err != nil {
		return nil, errf(CodeInternal, "could not read the texture", err)
	}
	out := make([]TextureView, 0, len(rows))
	for _, row := range rows {
		out = append(out, s.textureRowView(ctx, row))
	}
	return out, nil
}

func (s *Service) UploadTexture(ctx context.Context, uid int64, typ, model, desc string, png []byte, meta CallMeta) (TextureView, bool, *Error) {
	return s.uploadTexture(ctx, uid, typ, model, UploadMeta{Name: desc, Public: true}, png, meta)
}

// UploadTextureMeta is the metadata-aware upload used by the web form and the
// plugin bridge. Public controls the library dedupe/visibility path.
func (s *Service) UploadTextureMeta(ctx context.Context, uid int64, typ, model string, um UploadMeta, png []byte, meta CallMeta) (TextureView, bool, *Error) {
	return s.uploadTexture(ctx, uid, typ, model, um, png, meta)
}

// ImportPrivateTexture is the user-import path: it stores a private copy and
// deliberately skips the public-content check (the user needs an owned tid to
// apply it to a character). Same-owner duplicates remain idempotent.
func (s *Service) ImportPrivateTexture(ctx context.Context, uid int64, typ, model, desc string, png []byte, meta CallMeta) (TextureView, bool, *Error) {
	return s.uploadTexture(ctx, uid, typ, model, UploadMeta{Name: desc, Public: false}, png, meta)
}

func (s *Service) uploadTexture(ctx context.Context, uid int64, typ, model string, um UploadMeta, png []byte, meta CallMeta) (TextureView, bool, *Error) {
	if e := s.ensure(); e != nil {
		return TextureView{}, false, e
	}
	if typ != "skin" && typ != "cape" {
		return TextureView{}, false, errf(CodeMetadata, "texture type must be skin or cape", nil)
	}
	// The uploader's model claim is advisory only: clients keep sending it
	// (launcher-api, skin-importer, older builds) and an arm width the pixels do
	// not have renders wrong everywhere at once. Junk is still rejected.
	if typ == "skin" && model != "" && model != texture.ModelDefault && model != texture.ModelSlim {
		return TextureView{}, false, errf(CodeModelBad, "skin model must be default or slim", nil)
	}
	desc := strings.TrimSpace(um.Name)
	if !utf8.ValidString(desc) || !utf8.ValidString(um.DetailMD) ||
		!utf8.ValidString(um.Source) || !utf8.ValidString(um.SourceURL) {
		return TextureView{}, false, errf(CodeMetadata, "texture metadata must be UTF-8 text", nil)
	}
	if utf8.RuneCountInString(desc) > 50 {
		return TextureView{}, false, errf(CodeMetadata, "texture name is limited to 50 characters", nil)
	}
	detail := strings.TrimSpace(um.DetailMD)
	if utf8.RuneCountInString(detail) > 4000 {
		return TextureView{}, false, errf(CodeMetadata, "texture description is limited to 4000 characters", nil)
	}
	source := strings.ToLower(strings.TrimSpace(um.Source))
	sourceURL := strings.TrimSpace(um.SourceURL)
	switch source {
	case "", "original":
		sourceURL = ""
	case "derivative", "repost":
		if sourceURL == "" {
			return TextureView{}, false, errf(CodeSourceURLReq, "reposts and derivative works need the original URL", nil)
		}
		if !validSourceURL(sourceURL) {
			return TextureView{}, false, errf(CodeSourceURLBad, "the original URL must be an http(s) link", nil)
		}
	default:
		return TextureView{}, false, errf(CodeSourceBad, "texture source is not one of the allowed values", nil)
	}
	if source == "original" {
		sourceURL = ""
	}
	public := um.Public
	if desc != "" {
		if re, _, _ := s.Store.GetOption(ctx, "texture_name_regexp"); re != "" {
			if rx, err := regexp.Compile(re); err == nil && !rx.MatchString(desc) {
				return TextureView{}, false, errf(CodeMetadata, "texture name breaks the site naming rule", nil)
			}
		}
	}
	// The knob is in KiB and can only come down (hardMaxKB is the pipeline's
	// anti-bomb ceiling). handleUploadTexture caps the whole body at
	// MaxUploadBytes plus frame slack, so every value this knob can take is
	// actually reachable — the transport layer never arrives first with a
	// different number.
	limitKB := s.Store.MaxUploadKB(ctx, texture.MaxUploadBytes>>10)
	if int64(len(png)) > int64(limitKB)<<10 {
		return TextureView{}, false, errf(CodeTooLarge, "the file exceeds the upload size limit", texture.ErrTooLarge)
	}
	guardData := map[string]any{"filename": meta.Filename}
	if len(meta.PluginData) > 0 {
		guardData["pluginData"] = meta.PluginData
	}
	if ge := s.guard(ctx, "texture.upload.check", plugin.EventPayload{
		UserID: uid, Type: typ, IP: meta.IP, Data: guardData,
	}); ge != nil {
		return TextureView{}, false, ge
	}
	res, err := texture.Process(bytes.NewReader(png), texture.Type(typ))
	if err != nil {
		// One code per reason. A player who sent 100×100 needs to hear the
		// numbers they typed, not "the file could not be parsed" — that sentence
		// sends them back to the drawing board with no idea which door failed.
		var de *texture.DimensionsError
		switch {
		case errors.Is(err, texture.ErrTooLarge):
			return TextureView{}, false, errDomain(CodeTooLarge, err)
		case errors.As(err, &de):
			// The numbers go out as fields, not as the Go error string: the
			// dictionary puts them into a sentence in the player's language.
			return TextureView{}, false, errDetails(CodeDimensions, "the texture size is not one of the accepted families", map[string]any{
				"width": de.W, "height": de.H, "type": string(de.Typ),
			})
		case errors.Is(err, texture.ErrPixelBomb):
			return TextureView{}, false, errDomain(CodePixelBomb, err)
		default:
			return TextureView{}, false, errDomain(CodeInvalid, err)
		}
	}
	// One line per disagreement: the only way to notice that a launcher keeps
	// declaring the wrong model is that the log says so.
	if model != "" && model != res.Model {
		log.Printf("texture model override: uid=%d declared=%s detected=%s", uid, model, res.Model)
	}
	// The file is written before its row exists, and deletion unlinks it only
	// when no row refers to it — so both paths must hold the same content lock,
	// or an upload racing a delete leaves a row pointing at nothing.
	var written struct {
		tid    int64
		reuse  store.TextureRow
		dup    store.TextureRow
		reused bool
		dupped bool
	}
	failMsg := ""
	lockErr := s.Store.WithHashLock(ctx, res.Hash, func(ctx context.Context) error {
		if err := s.Textures.Save(res); err != nil {
			failMsg = "could not save the texture file"
			return err
		}
		existing, ok, err := s.Store.TextureByOwnerHash(ctx, uid, typ, res.Hash)
		if err != nil {
			return err
		}
		// Reusing a private import for a public upload would report success
		// while never publishing anything. Only short-circuit when the
		// existing row's visibility matches the request.
		if ok && !public {
			written.reuse, written.reused = existing, true
			return nil
		}
		if ok {
			if isPub, perr := s.Store.TextureIsPublic(ctx, existing.ID); perr == nil && isPub {
				written.reuse, written.reused = existing, true
				return nil
			}
		}
		if public {
			if pub, ok, err := s.Store.PublicTextureByHash(ctx, typ, res.Hash); err != nil {
				return err
			} else if ok {
				written.dup, written.dupped = pub, true
				return nil
			}
		}
		meta := store.TextureMeta{Description: desc, DetailMD: detail, Source: source, SourceURL: sourceURL}
		var addErr error
		if public {
			written.tid, addErr = s.Store.AddTexture(ctx, uid, typ, res.Hash, res.Model, meta)
		} else {
			written.tid, addErr = s.Store.AddTexturePrivate(ctx, uid, typ, res.Hash, res.Model, meta)
		}
		if addErr != nil {
			failMsg = "could not write the texture"
			if !errors.Is(addErr, store.ErrDuplicatePublic) {
				return addErr
			}
			// Lost the race against an identical upload: hand back the winner
			// rather than a conflict nobody can act on.
			if existing, ok, lookupErr := s.Store.TextureByOwnerHash(ctx, uid, typ, res.Hash); lookupErr == nil && ok {
				written.reuse, written.reused = existing, true
				return nil
			}
			if pub, ok, lookupErr := s.Store.PublicTextureByHash(ctx, typ, res.Hash); lookupErr == nil && ok {
				written.dup, written.dupped = pub, true
				return nil
			}
			return addErr
		}
		return nil
	})
	if lockErr != nil {
		if failMsg == "" {
			failMsg = "could not write the texture"
		}
		log.Printf("texture upload failed: %v", lockErr)
		if errors.Is(lockErr, store.ErrBusy) {
			return TextureView{}, false, errf(CodeBusy, "this texture is locked by another operation, try again shortly", lockErr)
		}
		return TextureView{}, false, errf(CodeInternal, failMsg, lockErr)
	}
	if written.reused {
		s.autoCloset(ctx, uid, written.reuse.ID)
		return s.textureRowView(ctx, written.reuse), false, nil
	}
	if written.dupped {
		return TextureView{}, false, s.duplicatePublic(written.dup)
	}
	tid := written.tid
	if s.OnTextureUploaded != nil {
		s.OnTextureUploaded(ctx, plugin.TextureUploadedEvent{UserID: uid, Hash: res.Hash, Type: typ})
	}
	if s.EmitEvent != nil {
		data := map[string]any{}
		if len(meta.PluginData) > 0 {
			data["pluginData"] = meta.PluginData
		}
		s.EmitEvent(ctx, "texture.uploaded", plugin.EventPayload{
			UserID: uid, Hash: res.Hash, Type: typ, PlayerName: meta.Filename, Data: data,
		})
	}
	s.audit(ctx, uid, meta, "", "plugin_texture_upload", map[string]any{
		"tid": tid, "hash": res.Hash, "type": typ,
	})
	s.autoCloset(ctx, uid, tid)
	return s.textureView(ctx, tid, typ, res.Hash, res.Model, desc, detail, source, sourceURL, time.Now()), true, nil
}

// autoCloset keeps blessing's invariant that an upload is immediately wearable:
// the closet is what the skin picker reads, so an upload that never lands there
// can only be worn through the ownership fallback. A failed closet write must
// not fail the upload — the texture row is the thing the user asked for.
func (s *Service) autoCloset(ctx context.Context, uid, tid int64) {
	if _, err := s.Store.AddClosetEntry(ctx, uid, tid, ""); err != nil {
		log.Printf("closet auto-add: uid=%d tid=%d: %v", uid, tid, err)
	}
}

func (s *Service) duplicatePublic(pub store.TextureRow) *Error {
	return errDetails(CodeDuplicatePub, "this skin is already in the public library", map[string]any{
		"textureId": pub.ID,
		"url":       siteurl.Page("skinlib", strconv.FormatInt(pub.ID, 10)),
	})
}
