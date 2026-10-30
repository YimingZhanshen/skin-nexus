package main

// pluginaccount.go adapts internal/account to the plugin runtime's
// AccountService. The service is uid-scoped by the caller; the adapter only
// fills request metadata and converts view shapes.
import (
	"context"
	"encoding/json"
	"errors"

	"skin-nexus/server/internal/account"
	"skin-nexus/server/internal/plugin"
	"skin-nexus/server/internal/store"
)

type pluginAccount struct {
	svc    *account.Service
	signer func(string) string
}

func convertView[T any](in any) T {
	var out T
	raw, err := json.Marshal(in)
	if err != nil {
		return out
	}
	_ = json.Unmarshal(raw, &out)
	return out
}

func pluginAccountErr(err error) error {
	var ae *account.Error
	if errors.As(err, &ae) {
		return &plugin.AccountError{Code: ae.Code, Message: ae.Message, Details: ae.Details}
	}
	return err
}

func (p pluginAccount) meta(m plugin.AccountCallMeta) account.CallMeta {
	signer := ""
	if p.signer != nil {
		signer = p.signer(m.Plugin)
	}
	return account.CallMeta{
		IP: m.IP, Filename: m.Filename, PluginData: m.PluginData,
		Plugin: m.Plugin, Version: m.Version, Signer: signer,
	}
}

func (p pluginAccount) Account(ctx context.Context, uid int64) (plugin.AccountView, error) {
	v, err := p.svc.Account(ctx, uid)
	if err != nil {
		return plugin.AccountView{}, pluginAccountErr(err)
	}
	return convertView[plugin.AccountView](v), nil
}

func (p pluginAccount) Limits(ctx context.Context) (plugin.SiteLimits, error) {
	v := p.svc.Limits(ctx)
	return plugin.SiteLimits{MaxUploadBytes: v.MaxUploadBytes, MaxPlayers: v.MaxPlayers, OIDCEnabled: v.OIDCEnabled}, nil
}

func (p pluginAccount) ListPlayers(ctx context.Context, uid int64) ([]plugin.PlayerView, error) {
	rows, err := p.svc.ListPlayers(ctx, uid)
	if err != nil {
		return nil, pluginAccountErr(err)
	}
	out := make([]plugin.PlayerView, 0, len(rows))
	for _, row := range rows {
		out = append(out, convertView[plugin.PlayerView](p.svc.PlayerViewOf(row)))
	}
	return out, nil
}

func (p pluginAccount) GetPlayer(ctx context.Context, uid, pid int64) (plugin.PlayerView, error) {
	v, err := p.svc.PlayerView(ctx, uid, pid)
	if err != nil {
		return plugin.PlayerView{}, pluginAccountErr(err)
	}
	return convertView[plugin.PlayerView](v), nil
}

func (p pluginAccount) CreatePlayer(ctx context.Context, uid int64, name string, meta plugin.AccountCallMeta) (plugin.PlayerView, bool, error) {
	v, created, err := p.svc.CreatePlayer(ctx, uid, name, p.meta(meta))
	if err != nil {
		return plugin.PlayerView{}, false, pluginAccountErr(err)
	}
	return convertView[plugin.PlayerView](v), created, nil
}

func (p pluginAccount) RenamePlayer(ctx context.Context, uid, pid int64, name string, meta plugin.AccountCallMeta) (plugin.PlayerView, bool, error) {
	v, invalidated, err := p.svc.RenamePlayer(ctx, uid, pid, name, p.meta(meta))
	if err != nil {
		return plugin.PlayerView{}, false, pluginAccountErr(err)
	}
	return convertView[plugin.PlayerView](v), invalidated, nil
}

func (p pluginAccount) DeletePlayer(ctx context.Context, uid, pid int64, meta plugin.AccountCallMeta) error {
	return pluginAccountErr(p.svc.DeletePlayer(ctx, uid, pid, p.meta(meta)))
}

func (p pluginAccount) SetTexture(ctx context.Context, uid, pid int64, kind string, tid int64, meta plugin.AccountCallMeta) (plugin.PlayerView, error) {
	v, err := p.svc.SetTexture(ctx, uid, pid, kind, tid, p.meta(meta))
	if err != nil {
		return plugin.PlayerView{}, pluginAccountErr(err)
	}
	return convertView[plugin.PlayerView](v), nil
}

func (p pluginAccount) ListTextures(ctx context.Context, uid int64) ([]plugin.TextureView, error) {
	rows, err := p.svc.ListTextures(ctx, uid)
	if err != nil {
		return nil, pluginAccountErr(err)
	}
	out := make([]plugin.TextureView, 0, len(rows))
	for _, row := range rows {
		out = append(out, convertView[plugin.TextureView](row))
	}
	return out, nil
}

func (p pluginAccount) UploadTexture(ctx context.Context, uid int64, typ, model, desc string, png []byte, meta plugin.AccountCallMeta) (plugin.TextureView, bool, error) {
	v, created, err := p.svc.UploadTexture(ctx, uid, typ, model, desc, png, p.meta(meta))
	if err != nil {
		return plugin.TextureView{}, false, pluginAccountErr(err)
	}
	return convertView[plugin.TextureView](v), created, nil
}

func (p pluginAccount) ImportPrivateTexture(ctx context.Context, uid int64, typ, model, desc string, png []byte, meta plugin.AccountCallMeta) (plugin.TextureView, bool, error) {
	v, created, err := p.svc.ImportPrivateTexture(ctx, uid, typ, model, desc, png, p.meta(meta))
	if err != nil {
		return plugin.TextureView{}, false, pluginAccountErr(err)
	}
	return convertView[plugin.TextureView](v), created, nil
}

func (p pluginAccount) ListLibrary(ctx context.Context, uid int64, q plugin.LibraryQuery) (plugin.LibraryPage, error) {
	v, err := p.svc.ListLibrary(ctx, uid, store.SkinlibOpts{
		Type: q.Type, Keyword: q.Keyword, Sort: q.Sort, Page: q.Page, PerPage: q.PerPage,
	})
	if err != nil {
		return plugin.LibraryPage{}, pluginAccountErr(err)
	}
	return convertView[plugin.LibraryPage](v), nil
}

func (p pluginAccount) GetLibrary(ctx context.Context, uid, tid int64) (plugin.LibraryItem, error) {
	v, err := p.svc.GetLibrary(ctx, uid, tid)
	if err != nil {
		return plugin.LibraryItem{}, pluginAccountErr(err)
	}
	return convertView[plugin.LibraryItem](v), nil
}

func (p pluginAccount) ImportLibrary(ctx context.Context, uid, tid int64, meta plugin.AccountCallMeta) (plugin.TextureView, bool, error) {
	v, created, err := p.svc.ImportLibraryTexture(ctx, uid, tid, p.meta(meta))
	if err != nil {
		return plugin.TextureView{}, false, pluginAccountErr(err)
	}
	return convertView[plugin.TextureView](v), created, nil
}
