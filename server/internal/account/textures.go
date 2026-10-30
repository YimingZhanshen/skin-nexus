package account

import (
	"bytes"
	"context"
	"errors"
	"fmt"
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
	if typ == "skin" {
		if model == "" {
			model = "default"
		}
		if model != "default" && model != "slim" {
			return TextureView{}, false, errf(CodeInvalid, "皮肤模型必须是 default 或 slim", nil)
		}
	} else {
		model = ""
	}
	desc := strings.TrimSpace(um.Name)
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
	res, err := texture.Process(bytes.NewReader(png), texture.Type(typ), model)
	if err != nil {
		if errors.Is(err, texture.ErrTooLarge) {
			return TextureView{}, false, errf(CodeTooLarge, err.Error(), err)
		}
		return TextureView{}, false, errf(CodeInvalid, err.Error(), err)
	}
	if err := s.Textures.Save(res); err != nil {
		return TextureView{}, false, errf(CodeInternal, "保存纹理失败", err)
	}
	if existing, ok, err := s.Store.TextureByOwnerHash(ctx, uid, typ, res.Hash); err == nil && ok {
		// Reusing a private import for a public upload would report success
		// while never publishing anything. Only short-circuit when the
		// existing row's visibility matches the request.
		if !public {
			return s.textureRowView(ctx, existing), false, nil
		}
		if isPub, perr := s.Store.TextureIsPublic(ctx, existing.ID); perr == nil && isPub {
			return s.textureRowView(ctx, existing), false, nil
		}
	}
	if public {
		if pub, ok, err := s.Store.PublicTextureByHash(ctx, typ, res.Hash); err == nil && ok {
			return TextureView{}, false, s.duplicatePublic(pub)
		}
	}
	var tid int64
	if public {
		tid, err = s.Store.AddTexture(ctx, uid, typ, res.Hash, res.Model, store.TextureMeta{
			Description: desc, DetailMD: detail, Source: source, SourceURL: sourceURL,
		})
	} else {
		tid, err = s.Store.AddTexturePrivate(ctx, uid, typ, res.Hash, res.Model, store.TextureMeta{
			Description: desc, DetailMD: detail, Source: source, SourceURL: sourceURL,
		})
	}
	if err != nil {
		if errors.Is(err, store.ErrDuplicatePublic) {
			if existing, ok, lookupErr := s.Store.TextureByOwnerHash(ctx, uid, typ, res.Hash); lookupErr == nil && ok {
				return s.textureRowView(ctx, existing), false, nil
			}
			if pub, ok, lookupErr := s.Store.PublicTextureByHash(ctx, typ, res.Hash); lookupErr == nil && ok {
				return TextureView{}, false, s.duplicatePublic(pub)
			}
		}
		return TextureView{}, false, errf(CodeInternal, "写入纹理失败", err)
	}
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
