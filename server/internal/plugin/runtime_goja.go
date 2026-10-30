package plugin

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"os"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/dop251/goja"

	"skin-nexus/server/internal/jobs"
	"skin-nexus/server/internal/outbound"
)

// errTimeout is pushed into the VM when a hook exceeds its budget.
var errTimeout = errors.New("plugin: hook timeout")

// typed hook function shapes (match the frozen contract callbacks).
type (
	profileHookFn   func(context.Context, *UserProfileFilter) error
	textureHookFn   func(context.Context, *TextureURLFilter) error
	uploadHookFn    func(context.Context, TextureUploadedEvent) error
	lifecycleHookFn func(context.Context, UserLifecycleEvent) error
	authHookFn      func(context.Context, AuthPhaseEvent) error
	avatarHookFn    func(context.Context, *AvatarFilter) error
)

// timed pairs a hook with its per-call budget.
type timed[T any] struct {
	fn      T
	timeout time.Duration
}

// hookSet is the per-target (runtime or host-extra) registration storage.
type hookSet struct {
	profile   []timed[profileHookFn]
	texture   []timed[textureHookFn]
	upload    []timed[uploadHookFn]
	lifecycle []timed[lifecycleHookFn]
	auth      []timed[authHookFn]
	avatar    []timed[avatarHookFn]
	events    map[string][]timed[eventFn]
	guards    map[string][]guardEntry
}

type eventFn func(context.Context, EventPayload) error

type guardFn func(context.Context, EventPayload) (GuardDecision, error)

type guardEntry struct {
	fn         guardFn
	timeout    time.Duration
	failClosed bool
}

// routeEntry is one plugin-contributed read-only route.
type routeEntry struct {
	method  string
	path    string // cleaned, leading "/"
	tier    RouteTier
	spec    RouteSpec
	fn      goja.Callable
	timeout time.Duration
}

// runtime owns a single plugin's goja VM and its registrations. goja is not
// goroutine-safe, so every VM interaction happens under mu.
type runtime struct {
	info Info
	vm   *goja.Runtime
	mu   sync.Mutex

	hooks  hookSet
	routes []routeEntry

	lastErr    string
	logs       []string
	calls      int64
	errors     int64
	timeouts   int64
	routeCalls int64

	// loadErr captures registration-time failures (bad permission, bad route)
	// so RunString can surface them as a load failure.
	loadErr error

	defaults    timeouts
	logSink     func(string)
	config      *configState
	store       *storeService
	netClient   *outbound.Scoped
	jobStore    *jobs.Store
	jobHandlers map[string]jobReg
	jobID       string
	jobUser     *SessionIdentity
	hostInfo    func() HostInfo
	account     func() AccountService
	routeUser   *SessionIdentity
	routeCtx    context.Context
	routeIP     string
	installFn   goja.Callable
	upgradeFn   goja.Callable
}

type timeouts struct {
	hook, auth, route, load time.Duration
}

func newRuntime(info Info, t timeouts, logSink func(string), cfg *configState, store *storeService, hostInfo func() HostInfo, account func() AccountService, jobStore *jobs.Store) (*runtime, error) {
	src, err := os.ReadFile(info.ScriptPath)
	if err != nil {
		return nil, fmt.Errorf("read entrypoint: %w", err)
	}
	rt := &runtime{info: info, vm: goja.New(), defaults: t, logSink: logSink, config: cfg, store: store, hostInfo: hostInfo, account: account, jobStore: jobStore, jobHandlers: map[string]jobReg{}}
	if info.hasPermission(PermNetHTTP) && info.Net != nil && info.Net.HTTP != nil {
		h := info.Net.HTTP
		timeout := time.Duration(h.TimeoutMS) * time.Millisecond
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		rt.netClient = outbound.NewScoped(outbound.Policy{
			Hosts: h.Hosts, AllowUserHosts: h.AllowUserHosts, UserHostPaths: h.UserHostPaths,
			MaxBytes: int64(h.MaxBytes), Timeout: timeout,
		})
	}
	rt.hooks.events = map[string][]timed[eventFn]{}
	rt.hooks.guards = map[string][]guardEntry{}
	rt.vm.SetMaxCallStackSize(1024)
	rt.installAPI()
	loadTimeout := t.load
	if loadTimeout <= 0 {
		loadTimeout = 2 * time.Second
	}
	timer := time.AfterFunc(loadTimeout, func() { rt.vm.Interrupt(errTimeout) })
	_, err = rt.vm.RunString(string(src))
	timer.Stop()
	rt.vm.ClearInterrupt()
	if err != nil {
		return nil, fmt.Errorf("run entrypoint: %w", err)
	}
	if rt.loadErr != nil {
		return nil, rt.loadErr
	}
	for _, spec := range info.Routes {
		if !rt.hasRoute(spec.Method, spec.Path) {
			return nil, fmt.Errorf("declared route %s %s was not registered by the entrypoint", spec.Method, spec.Path)
		}
	}
	for _, spec := range info.Jobs {
		if _, ok := rt.jobHandlers[spec.Name]; !ok {
			return nil, fmt.Errorf("declared job %s was not registered by the entrypoint", spec.Name)
		}
	}
	return rt, nil
}

func (rt *runtime) hasRoute(method, path string) bool {
	for _, r := range rt.routes {
		if r.method == method && r.path == path {
			return true
		}
	}
	return false
}

// installAPI exposes the global `skin` object plugins program against.
func (rt *runtime) installAPI() {
	rt.vm.Set("skin", map[string]interface{}{
		"hooks": map[string]interface{}{
			"onUserProfileFilter": rt.regHook(hookProfile),
			"onTextureURLFilter":  rt.regHook(hookTexture),
			"onTextureUploaded":   rt.regHook(hookUpload),
			"onUserLifecycle":     rt.regHook(hookLifecycle),
			"onAuthPhase":         rt.regHook(hookAuth),
			"onAvatarFilter":      rt.regHook(hookAvatar),
		},
		"route": rt.regRoute,
		"log":   rt.jsLog,
		"config": map[string]interface{}{
			"get": func(call goja.FunctionCall) goja.Value {
				if !rt.info.hasPermission(PermConfig) {
					rt.throw(errors.New("missing permission config"))
				}
				v, ok := rt.config.get(call.Argument(0).String())
				if !ok {
					return goja.Undefined()
				}
				return rt.vm.ToValue(v)
			},
			"all": func(call goja.FunctionCall) goja.Value {
				if !rt.info.hasPermission(PermConfig) {
					rt.throw(errors.New("missing permission config"))
				}
				return rt.vm.ToValue(rt.config.all())
			},
		},
		"store": rt.storeAPI(),
		"events": map[string]interface{}{
			"on": rt.onEvent,
		},
		"guards": map[string]interface{}{
			"on": rt.onGuard,
		},
		"host": map[string]interface{}{
			"info":      rt.jsHostInfo,
			"hasPlugin": rt.jsHasPlugin,
		},
		"net": map[string]interface{}{
			"get": rt.jsNetGet,
		},
		"jobs": map[string]interface{}{
			"register": rt.regJob,
			"submit":   rt.jsJobSubmit,
			"list":     rt.jsJobList,
			"status":   rt.jsJobStatus,
			"cancel":   rt.jsJobCancel,
			"resume":   rt.jsJobResume,
		},
		"account": map[string]interface{}{
			"get":    rt.jsAccountGet,
			"limits": rt.jsAccountLimits,
		},
		"players": map[string]interface{}{
			"list":       rt.jsPlayersList,
			"get":        rt.jsPlayersGet,
			"create":     rt.jsPlayersCreate,
			"rename":     rt.jsPlayersRename,
			"delete":     rt.jsPlayersDelete,
			"setTexture": rt.jsPlayersSetTexture,
		},
		"textures": map[string]interface{}{
			"list":          rt.jsTexturesList,
			"uploadBase64":  rt.jsTexturesUpload,
			"importBase64":  rt.jsTexturesImportBase64,
			"importLibrary": rt.jsTexturesImport,
		},
		"library": map[string]interface{}{
			"list": rt.jsLibraryList,
			"get":  rt.jsLibraryGet,
		},
	})
}

func (rt *runtime) jsNetGet(call goja.FunctionCall) goja.Value {
	if !rt.info.hasPermission(PermNetHTTP) {
		rt.throw(errors.New("missing permission net:http"))
	}
	if rt.netClient == nil {
		rt.throw(errors.New("net.unavailable: outbound surface is not configured"))
	}
	ctx := rt.routeCtx
	if ctx == nil {
		ctx = context.Background()
	}
	res, err := rt.netClient.Fetch(ctx, call.Argument(0).String())
	if err != nil {
		var pe *outbound.PolicyError
		if errors.As(err, &pe) {
			return rt.vm.ToValue(map[string]any{"ok": false, "error": map[string]any{"code": pe.Code, "message": pe.Message}})
		}
		return rt.vm.ToValue(map[string]any{"ok": false, "error": map[string]any{"code": "net.unavailable", "message": err.Error()}})
	}
	return rt.vm.ToValue(map[string]any{"ok": true, "data": map[string]any{
		"status": res.Status, "contentType": res.ContentType,
		"bytes": len(res.Body), "bodyBase64": base64.StdEncoding.EncodeToString(res.Body),
		"retryAfter": res.RetryAfter,
	}})
}

// jsHostInfo exposes the read-only host snapshot. It requires plugins:read so
// a plugin cannot enumerate the deployment for free.
func (rt *runtime) jsHostInfo(call goja.FunctionCall) goja.Value {
	if !rt.info.hasPermission(PermPluginsRead) {
		rt.throw(errors.New("missing permission plugins:read"))
	}
	info := HostInfo{}
	if rt.hostInfo != nil {
		info = rt.hostInfo()
	}
	plugins := make([]map[string]any, 0, len(info.Plugins))
	for _, p := range info.Plugins {
		plugins = append(plugins, map[string]any{
			"name": p.Name, "version": p.Version, "title": p.Title, "enabled": p.Enabled,
		})
	}
	return rt.vm.ToValue(map[string]any{
		"version":      info.Version,
		"hookApi":      info.HookAPI,
		"capabilities": info.Capabilities,
		"plugins":      plugins,
		"slots":        info.Slots,
	})
}

func (rt *runtime) jsHasPlugin(call goja.FunctionCall) goja.Value {
	if !rt.info.hasPermission(PermPluginsRead) {
		rt.throw(errors.New("missing permission plugins:read"))
	}
	name := call.Argument(0).String()
	if rt.hostInfo == nil {
		return rt.vm.ToValue(false)
	}
	for _, p := range rt.hostInfo().Plugins {
		if p.Name == name {
			return rt.vm.ToValue(true)
		}
	}
	return rt.vm.ToValue(false)
}

// hook kinds
const (
	hookProfile = iota
	hookTexture
	hookUpload
	hookLifecycle
	hookAuth
	hookAvatar
)

func hookPermission(kind int) string {
	switch kind {
	case hookProfile:
		return PermReadProfiles
	case hookTexture:
		return PermReadTextures
	case hookUpload:
		return PermEditTextures
	case hookLifecycle, hookAuth:
		return PermAuthHooks
	case hookAvatar:
		return PermMediaAvatar
	}
	return ""
}

func (rt *runtime) defaultTimeout(kind int) time.Duration {
	if kind == hookAuth || kind == hookAvatar {
		return rt.defaults.auth
	}
	return rt.defaults.hook
}

// regHook returns the JS-facing registration function for one hook kind.
func (rt *runtime) regHook(kind int) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		cb, ok := goja.AssertFunction(call.Argument(0))
		if !ok {
			rt.loadErr = errors.New("hook callback must be a function")
			return goja.Undefined()
		}
		perm := hookPermission(kind)
		if perm != "" && !rt.info.hasPermission(perm) {
			rt.loadErr = fmt.Errorf("missing permission %q for hook", perm)
			return goja.Undefined()
		}
		d := rt.defaultTimeout(kind)
		if ms := optTimeoutMS(rt.vm, call.Argument(1)); ms > 0 {
			if td := time.Duration(ms) * time.Millisecond; td < d {
				d = td // plugins may tighten, never loosen
			}
		}
		opts := HostOptions{TimeoutMS: int(d.Milliseconds())}
		switch kind {
		case hookProfile:
			_ = rt.OnUserProfileFilter(rt.wrapProfile(cb, d), opts)
		case hookTexture:
			_ = rt.OnTextureURLFilter(rt.wrapTexture(cb, d), opts)
		case hookUpload:
			_ = rt.OnTextureUploaded(rt.wrapUpload(cb, d), opts)
		case hookLifecycle:
			_ = rt.OnUserLifecycle(rt.wrapLifecycle(cb, d), opts)
		case hookAuth:
			_ = rt.OnAuthPhase(rt.wrapAuth(cb, d), opts)
		case hookAvatar:
			_ = rt.OnAvatarFilter(rt.wrapAvatar(cb, d), opts)
		}
		return goja.Undefined()
	}
}

func optTimeoutMS(vm *goja.Runtime, v goja.Value) int {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return 0
	}
	obj := v.ToObject(vm)
	if obj == nil {
		return 0
	}
	tv := obj.Get("timeoutMS")
	if tv == nil || goja.IsUndefined(tv) {
		return 0
	}
	return int(tv.ToInteger())
}

// withTimeout runs a VM closure under an interrupt deadline.
func (rt *runtime) withTimeout(d time.Duration, run func() (goja.Value, error)) (goja.Value, error) {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	if rt.vm == nil {
		return nil, errors.New("plugin: runtime closed")
	}
	rt.calls++
	vm := rt.vm
	var guard sync.Mutex
	done := false
	timer := time.AfterFunc(d, func() {
		guard.Lock()
		defer guard.Unlock()
		if !done {
			vm.Interrupt(errTimeout)
		}
	})
	defer func() {
		guard.Lock()
		done = true
		guard.Unlock()
		timer.Stop()
		vm.ClearInterrupt()
	}()
	v, err := run()
	if err != nil {
		rt.errors++
		if errors.Is(err, errTimeout) {
			rt.timeouts++
		}
	}
	return v, err
}

func (rt *runtime) record(err error) {
	msg := err.Error()
	if len(msg) > 512 {
		msg = msg[:512]
	}
	rt.lastErr = msg
	rt.logs = appendBounded(rt.logs, "[error] "+msg)
	if rt.logSink != nil {
		rt.logSink(fmt.Sprintf("plugin %s: %s", rt.info.Name, msg))
	}
}

func appendBounded(s []string, v string) []string {
	s = append(s, v)
	if len(s) > 64 {
		s = s[len(s)-64:]
	}
	return s
}

// wrapProfile converts a JS filter into the contract's profile-filter shape.
func (rt *runtime) wrapProfile(cb goja.Callable, d time.Duration) profileHookFn {
	return func(ctx context.Context, p *UserProfileFilter) error {
		val, err := rt.withTimeout(d, func() (goja.Value, error) {
			return cb(goja.Undefined(), rt.vm.ToValue(map[string]interface{}{
				"playerId":   p.PlayerID,
				"playerName": p.PlayerName,
				"uuid":       p.UUID,
				"textures": map[string]interface{}{
					"SKIN": p.TexturesURL["SKIN"],
					"CAPE": p.TexturesURL["CAPE"],
				},
			}))
		})
		_ = ctx
		if err != nil {
			rt.record(err)
			return nil // fail-open
		}
		if val == nil || goja.IsUndefined(val) || goja.IsNull(val) {
			return nil
		}
		obj, ok := val.Export().(map[string]interface{})
		if !ok {
			return nil
		}
		if p.TexturesURL == nil {
			p.TexturesURL = map[string]string{}
		}
		for _, k := range []string{"SKIN", "CAPE"} {
			rv, present := obj[k]
			if !present {
				continue
			}
			s, isStr := rv.(string)
			if !isStr {
				continue
			}
			if s == "" {
				delete(p.TexturesURL, k)
			} else {
				p.TexturesURL[k] = s
			}
		}
		return nil
	}
}

// wrapTexture converts a JS URL filter into the contract's texture-filter shape.
func (rt *runtime) wrapTexture(cb goja.Callable, d time.Duration) textureHookFn {
	return func(ctx context.Context, t *TextureURLFilter) error {
		val, err := rt.withTimeout(d, func() (goja.Value, error) {
			return cb(goja.Undefined(), rt.vm.ToValue(map[string]interface{}{
				"hash": t.Hash, "url": t.URL, "type": t.Type,
			}))
		})
		_ = ctx
		if err != nil {
			rt.record(err)
			return nil
		}
		if val == nil || goja.IsUndefined(val) || goja.IsNull(val) {
			return nil
		}
		if s, isStr := val.Export().(string); isStr && s != "" {
			t.URL = s
		}
		return nil
	}
}

func (rt *runtime) wrapUpload(cb goja.Callable, d time.Duration) uploadHookFn {
	return func(ctx context.Context, e TextureUploadedEvent) error {
		_, err := rt.withTimeout(d, func() (goja.Value, error) {
			return cb(goja.Undefined(), rt.vm.ToValue(map[string]interface{}{
				"userId": e.UserID, "playerId": e.PlayerID, "hash": e.Hash, "type": e.Type,
			}))
		})
		_ = ctx
		if err != nil {
			rt.record(err)
		}
		return nil
	}
}

func (rt *runtime) wrapLifecycle(cb goja.Callable, d time.Duration) lifecycleHookFn {
	return func(ctx context.Context, e UserLifecycleEvent) error {
		_, err := rt.withTimeout(d, func() (goja.Value, error) {
			return cb(goja.Undefined(), rt.vm.ToValue(map[string]interface{}{
				"kind": e.Kind, "userId": e.UserID,
			}))
		})
		_ = ctx
		if err != nil {
			rt.record(err)
		}
		return nil
	}
}

func (rt *runtime) wrapAuth(cb goja.Callable, d time.Duration) authHookFn {
	return func(ctx context.Context, e AuthPhaseEvent) error {
		_, err := rt.withTimeout(d, func() (goja.Value, error) {
			return cb(goja.Undefined(), rt.vm.ToValue(map[string]interface{}{
				"phase": e.Phase, "uuid": e.UUID, "player": e.Player,
			}))
		})
		_ = ctx
		if err != nil {
			rt.record(err)
		}
		return nil
	}
}

// wrapAvatar converts a JS avatar filter into the contract shape. Only a
// non-empty string return counts; everything else leaves the PNG path intact.
func (rt *runtime) wrapAvatar(cb goja.Callable, d time.Duration) avatarHookFn {
	return func(ctx context.Context, a *AvatarFilter) error {
		val, err := rt.withTimeout(d, func() (goja.Value, error) {
			return cb(goja.Undefined(), rt.vm.ToValue(map[string]interface{}{
				"hash": a.Hash, "size": a.Size, "threeD": a.ThreeD,
			}))
		})
		_ = ctx
		if err != nil {
			rt.record(err)
			return nil
		}
		if val == nil || goja.IsUndefined(val) || goja.IsNull(val) {
			return nil
		}
		if s, ok := val.Export().(string); ok {
			a.URL = s
		}
		return nil
	}
}

// regRoute validates and stores a plugin-contributed route.
func (rt *runtime) regRoute(call goja.FunctionCall) goja.Value {
	method := strings.ToUpper(strings.TrimSpace(call.Argument(0).String()))
	rawPath := call.Argument(1).String()
	cb, ok := goja.AssertFunction(call.Argument(2))
	if !ok {
		rt.loadErr = errors.New("route handler must be a function")
		return goja.Undefined()
	}
	clean := path.Clean("/" + strings.TrimPrefix(rawPath, "/"))
	if clean == "/" || hasDotDotSegment(clean) {
		rt.loadErr = fmt.Errorf("invalid route path %q", rawPath)
		return goja.Undefined()
	}
	spec, found := findManifestRoute(rt.info.Routes, method, clean)
	if !found {
		rt.loadErr = fmt.Errorf("route %s %s is not declared in plugin.json", method, clean)
		return goja.Undefined()
	}
	tier := RouteTier(spec.Tier)
	if tier == "" {
		tier = RouteTierPublic
	}
	if !routeTierPermissionOK(rt.info.Manifest, tier) {
		rt.loadErr = fmt.Errorf("missing permission for route tier %q", tier)
		return goja.Undefined()
	}
	rt.routes = append(rt.routes, routeEntry{method: method, path: clean, tier: tier, spec: spec, fn: cb, timeout: routeTimeout(spec, rt.defaults.route)})
	if len(rt.routes) > maxRoutesPerPlugin {
		rt.loadErr = errors.New("too many routes")
		return goja.Undefined()
	}
	for i := 0; i < len(rt.routes)-1; i++ {
		r := rt.routes[i]
		if r.method == method && r.path == clean {
			rt.loadErr = fmt.Errorf("duplicate route %s %s", method, clean)
			return goja.Undefined()
		}
	}
	return goja.Undefined()
}

// routeTimeout applies a manifest-declared budget (100ms..10s), falling back
// to the host route default. Long budgets are only sensible for binary uploads
// whose Go-side processing cannot be interrupted by vm.Interrupt.
func routeTimeout(spec RouteSpec, def time.Duration) time.Duration {
	if spec.TimeoutMS <= 0 {
		return def
	}
	d := time.Duration(spec.TimeoutMS) * time.Millisecond
	if d < 100*time.Millisecond {
		d = 100 * time.Millisecond
	}
	if d > 10*time.Second {
		d = 10 * time.Second
	}
	return d
}

func findManifestRoute(routes []RouteSpec, method, path string) (RouteSpec, bool) {
	for _, r := range routes {
		if r.Method == method && r.Path == path {
			return r, true
		}
	}
	return RouteSpec{}, false
}

func routeTierPermissionOK(m Manifest, tier RouteTier) bool {
	switch tier {
	case RouteTierPublic:
		return m.hasPermission(PermRegisterRoute)
	case RouteTierSession:
		return m.hasPermission(PermHTTPRoutesSession)
	case RouteTierAdmin:
		return m.hasPermission(PermHTTPRoutesAdmin)
	case RouteTierSuperAdmin:
		return m.hasPermission(PermHTTPRoutesSuper)
	case RouteTierBearer:
		return m.hasPermission(PermHTTPRoutesBearer)
	}
	return false
}

// jsLog appends plugin log lines (bounded) and mirrors to the host logger.
func (rt *runtime) jsLog(call goja.FunctionCall) goja.Value {
	parts := make([]string, 0, len(call.Arguments))
	for _, a := range call.Arguments {
		parts = append(parts, a.String())
	}
	line := strings.Join(parts, " ")
	rt.logs = appendBounded(rt.logs, line)
	if rt.logSink != nil {
		rt.logSink(fmt.Sprintf("plugin %s: %s", rt.info.Name, line))
	}
	log.Printf("plugin %s: %s", rt.info.Name, line)
	return goja.Undefined()
}

// callRoute executes one stored route and converts the JS return value.
func (rt *runtime) callRoute(ctx context.Context, e routeEntry, req map[string]interface{}, id *SessionIdentity) (int, interface{}) {
	rt.mu.Lock()
	rt.routeCalls++
	rt.mu.Unlock()
	ip, _ := req["ip"].(string)
	val, err := rt.withTimeout(e.timeout, func() (goja.Value, error) {
		rt.routeUser = id
		rt.routeCtx = ctx
		rt.routeIP = ip
		defer func() {
			rt.routeUser = nil
			rt.routeCtx = nil
			rt.routeIP = ""
		}()
		return e.fn(goja.Undefined(), rt.vm.ToValue(req))
	})
	_ = ctx
	if err != nil {
		rt.record(err)
		return 503, map[string]interface{}{"error": "plugin route unavailable"}
	}
	if val == nil || goja.IsUndefined(val) {
		return 200, map[string]interface{}{}
	}
	exported := val.Export()
	if obj, ok := exported.(map[string]interface{}); ok {
		if st, hasStatus := obj["status"]; hasStatus {
			code := 200
			switch n := st.(type) {
			case int64:
				code = int(n)
			case float64:
				code = int(n)
			}
			return code, obj["body"]
		}
	}
	return 200, exported
}

// close releases the VM. Callers must ensure no in-flight calls remain.
func (rt *runtime) close() {
	rt.mu.Lock()
	defer rt.mu.Unlock()
	rt.vm = nil
}
