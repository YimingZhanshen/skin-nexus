package account

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"regexp"
	"strings"
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
		return nil, errf(CodeInternal, "读取纹理失败", err)
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
		return TextureView{}, false, errf(CodeInvalid, "纹理类型必须是 skin 或 cape", nil)
	}
	// The uploader's model claim is advisory only: clients keep sending it
	// (launcher-api, skin-importer, older builds) and an arm width the pixels do
	// not have renders wrong everywhere at once. Junk is still rejected.
	if typ == "skin" && model != "" && model != texture.ModelDefault && model != texture.ModelSlim {
		return TextureView{}, false, errf(CodeInvalid, "皮肤模型必须是 default 或 slim", nil)
	}
	desc := strings.TrimSpace(um.Name)
	if !utf8.ValidString(desc) || !utf8.ValidString(um.DetailMD) ||
		!utf8.ValidString(um.Source) || !utf8.ValidString(um.SourceURL) {
		return TextureView{}, false, errf(CodeInvalid, "材质信息必须是 UTF-8 文本", nil)
	}
	if utf8.RuneCountInString(desc) > 50 {
		return TextureView{}, false, errf(CodeInvalid, "纹理备注最多 50 个字", nil)
	}
	detail := strings.TrimSpace(um.DetailMD)
	if utf8.RuneCountInString(detail) > 4000 {
		return TextureView{}, false, errf(CodeInvalid, "材质描述最多 4000 个字", nil)
	}
	source := strings.ToLower(strings.TrimSpace(um.Source))
	sourceURL := strings.TrimSpace(um.SourceURL)
	switch source {
	case "", "original":
		sourceURL = ""
	case "derivative", "repost":
		if sourceURL == "" {
			return TextureView{}, false, errf(CodeSourceURLReq, "二创/转载必须填写原始发布地址", nil)
		}
		if !validSourceURL(sourceURL) {
			return TextureView{}, false, errf(CodeSourceURLBad, "原始发布地址必须是 http(s) 链接", nil)
		}
	default:
		return TextureView{}, false, errf(CodeInvalid, "材质来源不合法", nil)
	}
	if source == "original" {
		sourceURL = ""
	}
	public := um.Public
	if desc != "" {
		if re, _, _ := s.Store.GetOption(ctx, "texture_name_regexp"); re != "" {
			if rx, err := regexp.Compile(re); err == nil && !rx.MatchString(desc) {
				return TextureView{}, false, errf(CodeInvalid, "材质备注不符合命名规则", nil)
			}
		}
	}
	limitKB := s.Store.MaxUploadKB(ctx, texture.MaxUploadBytes>>10)
	if int64(len(png)) > int64(limitKB)<<10 {
		return TextureView{}, false, errf(CodeTooLarge, "文件超过上传大小上限", texture.ErrTooLarge)
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
		if errors.Is(err, texture.ErrTooLarge) {
			return TextureView{}, false, errf(CodeTooLarge, err.Error(), err)
		}
		return TextureView{}, false, errf(CodeInvalid, err.Error(), err)
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
			failMsg = "保存纹理失败"
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
			failMsg = "写入纹理失败"
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
			failMsg = "写入纹理失败"
		}
		log.Printf("texture upload failed: %v", lockErr)
		if errors.Is(lockErr, store.ErrBusy) {
			return TextureView{}, false, errf(CodeBusy, "该材质正被其他操作占用，请稍后重试", lockErr)
		}
		return TextureView{}, false, errf(CodeInternal, failMsg, lockErr)
	}
	if written.reused {
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
	return s.textureView(ctx, tid, typ, res.Hash, res.Model, desc, detail, source, sourceURL, time.Now()), true, nil
}

func (s *Service) duplicatePublic(pub store.TextureRow) *Error {
	return errDetails(CodeDuplicatePub, "该皮肤已在公共皮肤库", map[string]any{
		"textureId": pub.ID,
		"url":       fmt.Sprintf("%s/skinlib/%d", s.base(), pub.ID),
	})
}
