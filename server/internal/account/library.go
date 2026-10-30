package account

import (
	"context"
	"errors"
	"time"

	"skin-nexus/server/internal/store"
)

func (s *Service) libraryItem(ctx context.Context, uid int64, row store.SkinlibRow) LibraryItem {
	item := LibraryItem{
		TID: row.TID, Name: row.Name, Type: row.Type, Model: row.Model, Hash: row.Hash,
		Source: row.Source, SourceURL: row.SourceURL, DetailMD: row.DetailMD,
		Uploader: row.Nickname, Likes: row.Likes, Liked: row.Liked, Public: row.Public,
		Owned: row.UID == uid, TextureURL: s.textureURL(row.Hash),
	}
	if row.Type == "skin" {
		item.PreviewURL = s.previewURL(row.TID, "skin")
		item.AvatarURL = s.avatarURL(row.Hash)
	}
	if row.UploadAt.Valid {
		item.UploadedAt = row.UploadAt.Time.UTC().Format(time.RFC3339)
	}
	return item
}

func (s *Service) ListLibrary(ctx context.Context, uid int64, opt store.SkinlibOpts) (LibraryPage, *Error) {
	if e := s.ensure(); e != nil {
		return LibraryPage{}, e
	}
	opt.ViewerUID = uid
	opt.ViewerAdmin = false
	opt.PublicOnly = true
	if opt.PerPage <= 0 || opt.PerPage > 50 {
		opt.PerPage = 20
	}
	rows, total, err := s.Store.SkinlibList(ctx, opt)
	if err != nil {
		return LibraryPage{}, errf(CodeInternal, "读取皮肤库失败", err)
	}
	page := opt.Page
	if page <= 0 {
		page = 1
	}
	items := make([]LibraryItem, 0, len(rows))
	for _, row := range rows {
		items = append(items, s.libraryItem(ctx, uid, row))
	}
	return LibraryPage{Items: items, Total: total, Page: page, PerPage: opt.PerPage}, nil
}

func (s *Service) GetLibrary(ctx context.Context, uid, tid int64) (LibraryItem, *Error) {
	if e := s.ensure(); e != nil {
		return LibraryItem{}, e
	}
	row, err := s.Store.SkinlibOne(ctx, tid, uid, false)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return LibraryItem{}, errf(CodeLibraryNotFnd, "纹理不存在", err)
		}
		return LibraryItem{}, errf(CodeInternal, "读取纹理失败", err)
	}
	if !row.Public && row.UID != uid {
		return LibraryItem{}, errf(CodeLibraryNotFnd, "纹理不存在", nil)
	}
	return s.libraryItem(ctx, uid, row), nil
}

// ImportLibraryTexture copies a public texture row into the caller's private
// library. The content-addressed file is shared; no bytes are duplicated.
func (s *Service) ImportLibraryTexture(ctx context.Context, uid, tid int64, meta CallMeta) (TextureView, bool, *Error) {
	if e := s.ensure(); e != nil {
		return TextureView{}, false, e
	}
	row, err := s.Store.SkinlibOne(ctx, tid, uid, false)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return TextureView{}, false, errf(CodeLibraryNotFnd, "纹理不存在", err)
		}
		return TextureView{}, false, errf(CodeInternal, "读取纹理失败", err)
	}
	if !row.Public && row.UID != uid {
		return TextureView{}, false, errf(CodeLibraryNotFnd, "纹理不存在", nil)
	}
	if existing, ok, err := s.Store.TextureByOwnerHash(ctx, uid, row.Type, row.Hash); err == nil && ok {
		return s.textureRowView(ctx, existing), false, nil
	}
	model := row.Model
	if row.Type != "skin" {
		model = ""
	}
	newTID, err := s.Store.AddTexturePrivate(ctx, uid, row.Type, row.Hash, model, store.TextureMeta{
		Description: row.Name, DetailMD: row.DetailMD, Source: row.Source, SourceURL: row.SourceURL,
	})
	if err != nil {
		return TextureView{}, false, errf(CodeInternal, "导入纹理失败", err)
	}
	s.audit(ctx, uid, meta, "", "plugin_texture_import", map[string]any{
		"tid": newTID, "sourceTid": tid, "hash": row.Hash, "type": row.Type,
	})
	return s.textureView(ctx, newTID, row.Type, row.Hash, model, row.Name,
		row.DetailMD, row.Source, row.SourceURL, time.Now()), true, nil
}
