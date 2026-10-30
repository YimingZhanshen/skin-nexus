package plugin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dop251/goja"
)

func (rt *runtime) accountContext(perm string) (AccountService, *SessionIdentity, context.Context) {
	if !rt.info.hasPermission(perm) {
		rt.throw(fmt.Errorf("missing permission %s", perm))
	}
	id := rt.routeUser
	if id == nil {
		id = rt.jobUser
	}
	if id == nil || id.ID == 0 || rt.routeCtx == nil {
		rt.throw(errors.New("account.required: authenticated route required"))
	}
	var svc AccountService
	if rt.account != nil {
		svc = rt.account()
	}
	if svc == nil {
		rt.throw(errors.New("account.unavailable: account bridge is not configured"))
	}
	return svc, id, rt.routeCtx
}

func (rt *runtime) accountMeta(filename string, data map[string]any) AccountCallMeta {
	return AccountCallMeta{
		IP: rt.routeIP, Filename: filename, PluginData: data,
		Plugin: rt.info.Name, Version: rt.info.Version,
	}
}

func toJSValue(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}

func (rt *runtime) jsOK(v any) goja.Value {
	return rt.vm.ToValue(map[string]any{"ok": true, "data": toJSValue(v)})
}

func (rt *runtime) jsOKExtra(v any, extra map[string]any) goja.Value {
	out := map[string]any{"ok": true, "data": toJSValue(v)}
	for k, val := range extra {
		out[k] = val
	}
	return rt.vm.ToValue(out)
}

func (rt *runtime) jsFail(err error) goja.Value {
	var ae *AccountError
	if errors.As(err, &ae) {
		out := map[string]any{"code": ae.Code, "message": ae.Message}
		if len(ae.Details) > 0 {
			out["details"] = ae.Details
		}
		return rt.vm.ToValue(map[string]any{"ok": false, "error": out})
	}
	return rt.vm.ToValue(map[string]any{"ok": false, "error": map[string]any{"code": "internal", "message": err.Error()}})
}

func pidArg(call goja.FunctionCall) int64 { return call.Argument(0).ToInteger() }

func (rt *runtime) jsAccountGet(call goja.FunctionCall) goja.Value {
	svc, id, ctx := rt.accountContext(PermAccountRead)
	v, err := svc.Account(ctx, id.ID)
	if err != nil {
		return rt.jsFail(err)
	}
	return rt.jsOK(v)
}

func (rt *runtime) jsAccountLimits(call goja.FunctionCall) goja.Value {
	svc, _, ctx := rt.accountContext(PermAccountRead)
	v, err := svc.Limits(ctx)
	if err != nil {
		return rt.jsFail(err)
	}
	return rt.jsOK(v)
}

func (rt *runtime) jsPlayersList(call goja.FunctionCall) goja.Value {
	svc, id, ctx := rt.accountContext(PermPlayersRead)
	v, err := svc.ListPlayers(ctx, id.ID)
	if err != nil {
		return rt.jsFail(err)
	}
	return rt.jsOK(v)
}

func (rt *runtime) jsPlayersGet(call goja.FunctionCall) goja.Value {
	svc, id, ctx := rt.accountContext(PermPlayersRead)
	v, err := svc.GetPlayer(ctx, id.ID, pidArg(call))
	if err != nil {
		return rt.jsFail(err)
	}
	return rt.jsOK(v)
}

func (rt *runtime) jsPlayersCreate(call goja.FunctionCall) goja.Value {
	svc, id, ctx := rt.accountContext(PermPlayersWrite)
	v, created, err := svc.CreatePlayer(ctx, id.ID, call.Argument(0).String(), rt.accountMeta("", nil))
	if err != nil {
		return rt.jsFail(err)
	}
	return rt.jsOKExtra(v, map[string]any{"created": created})
}

func (rt *runtime) jsPlayersRename(call goja.FunctionCall) goja.Value {
	svc, id, ctx := rt.accountContext(PermPlayersWrite)
	pid := pidArg(call)
	name := call.Argument(1).String()
	v, invalidated, err := svc.RenamePlayer(ctx, id.ID, pid, name, rt.accountMeta("", nil))
	if err != nil {
		return rt.jsFail(err)
	}
	return rt.jsOKExtra(v, map[string]any{"tokensInvalidated": invalidated})
}

func (rt *runtime) jsPlayersDelete(call goja.FunctionCall) goja.Value {
	svc, id, ctx := rt.accountContext(PermPlayersWrite)
	if err := svc.DeletePlayer(ctx, id.ID, pidArg(call), rt.accountMeta("", nil)); err != nil {
		return rt.jsFail(err)
	}
	return rt.vm.ToValue(map[string]any{"ok": true})
}

func (rt *runtime) jsPlayersSetTexture(call goja.FunctionCall) goja.Value {
	svc, id, ctx := rt.accountContext(PermPlayersWrite)
	pid := pidArg(call)
	kind := call.Argument(1).String()
	tid := call.Argument(2).ToInteger()
	v, err := svc.SetTexture(ctx, id.ID, pid, kind, tid, rt.accountMeta("", nil))
	if err != nil {
		return rt.jsFail(err)
	}
	return rt.jsOK(v)
}

func (rt *runtime) jsTexturesList(call goja.FunctionCall) goja.Value {
	svc, id, ctx := rt.accountContext(PermReadTextures)
	v, err := svc.ListTextures(ctx, id.ID)
	if err != nil {
		return rt.jsFail(err)
	}
	return rt.jsOK(v)
}

func (rt *runtime) jsUploadMeta(call goja.FunctionCall, desc string, public bool) UploadMeta {
	um := UploadMeta{Name: desc, Public: public}
	v := call.Argument(4)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return um
	}
	obj := v.ToObject(rt.vm)
	if x := obj.Get("detail"); x != nil && !goja.IsUndefined(x) {
		um.DetailMD = x.String()
	}
	if x := obj.Get("source"); x != nil && !goja.IsUndefined(x) {
		um.Source = x.String()
	}
	if x := obj.Get("sourceUrl"); x != nil && !goja.IsUndefined(x) {
		um.SourceURL = x.String()
	}
	if x := obj.Get("public"); x != nil && !goja.IsUndefined(x) {
		um.Public = x.ToBoolean()
	}
	return um
}

func (rt *runtime) jsTexturesUpload(call goja.FunctionCall) goja.Value {
	svc, id, ctx := rt.accountContext(PermEditTextures)
	typ := call.Argument(0).String()
	model := call.Argument(1).String()
	desc := call.Argument(2).String()
	raw, err := base64.StdEncoding.DecodeString(call.Argument(3).String())
	if err != nil {
		return rt.jsFail(&AccountError{Code: "texture.invalid", Message: "invalid base64 payload"})
	}
	filename := "upload.png"
	if typ != "" {
		filename = typ + ".png"
	}
	v, created, uerr := svc.UploadTextureMeta(ctx, id.ID, typ, model, rt.jsUploadMeta(call, desc, true), raw, rt.accountMeta(filename, nil))
	if uerr != nil {
		return rt.jsFail(uerr)
	}
	return rt.jsOKExtra(v, map[string]any{"created": created})
}

func (rt *runtime) jsTexturesImport(call goja.FunctionCall) goja.Value {
	svc, id, ctx := rt.accountContext(PermEditTextures)
	v, created, err := svc.ImportLibrary(ctx, id.ID, pidArg(call), rt.accountMeta("", nil))
	if err != nil {
		return rt.jsFail(err)
	}
	return rt.jsOKExtra(v, map[string]any{"created": created})
}

// jsTexturesImportBase64 stores an externally fetched PNG as a PRIVATE
// texture owned by the caller (used by import plugins). It intentionally
// bypasses the public-library duplicate rejection so the user can apply an
// already-public image to their own character.
func (rt *runtime) jsTexturesImportBase64(call goja.FunctionCall) goja.Value {
	svc, id, ctx := rt.accountContext(PermEditTextures)
	typ := call.Argument(0).String()
	model := call.Argument(1).String()
	desc := call.Argument(2).String()
	raw, err := base64.StdEncoding.DecodeString(call.Argument(3).String())
	if err != nil {
		return rt.jsFail(&AccountError{Code: "texture.invalid", Message: "invalid base64 payload"})
	}
	filename := "import.png"
	if typ != "" {
		filename = typ + ".png"
	}
	um := rt.jsUploadMeta(call, desc, false)
	um.Public = false // importBase64 is always private, even if JS passes public:true
	v, created, uerr := svc.UploadTextureMeta(ctx, id.ID, typ, model, um, raw, rt.accountMeta(filename, nil))
	if uerr != nil {
		return rt.jsFail(uerr)
	}
	return rt.jsOKExtra(v, map[string]any{"created": created})
}

func (rt *runtime) jsLibraryList(call goja.FunctionCall) goja.Value {
	svc, id, ctx := rt.accountContext(PermReadTextures)
	q := LibraryQuery{}
	if opts := call.Argument(0); !goja.IsUndefined(opts) && !goja.IsNull(opts) {
		q.Type = stringField(rt.vm, opts, "type")
		q.Keyword = stringField(rt.vm, opts, "q")
		q.Sort = stringField(rt.vm, opts, "sort")
		q.Page = intField(rt.vm, opts, "page")
		q.PerPage = intField(rt.vm, opts, "perPage")
	}
	v, err := svc.ListLibrary(ctx, id.ID, q)
	if err != nil {
		return rt.jsFail(err)
	}
	return rt.jsOK(v)
}

func (rt *runtime) jsLibraryGet(call goja.FunctionCall) goja.Value {
	svc, id, ctx := rt.accountContext(PermReadTextures)
	v, err := svc.GetLibrary(ctx, id.ID, pidArg(call))
	if err != nil {
		return rt.jsFail(err)
	}
	return rt.jsOK(v)
}

func stringField(vm *goja.Runtime, obj goja.Value, name string) string {
	v := obj.ToObject(vm).Get(name)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return ""
	}
	return v.String()
}

func intField(vm *goja.Runtime, obj goja.Value, name string) int {
	v := obj.ToObject(vm).Get(name)
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return 0
	}
	return int(v.ToInteger())
}
