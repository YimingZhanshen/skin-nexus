// Package plugin defines the phase-1 frozen hook contract and the PluginHost
// interface. The runtime (goja, frozen by the M0 spike) plugs in behind this
// contract; process-outside hosts can be added later without changing hooks.
//
// Contract versioning: hooks are additive-versioned (SkinHookV1...). Plugins
// declare the hook API version in manifest.json; the host refuses to load a
// plugin requiring a newer major. Permission manifest is DECLARATIVE +
// AUDITED, not enforced isolation (phase-1 trust model: reviewed plugins).
package plugin

import "context"

// HookAPIVersion is the single, current plugin contract major. There is no
// legacy version to support: this project has no pre-existing third-party
// plugin ecosystem yet.
const HookAPIVersion = 2

// Manifest is the plugin's plugin.json. All service blocks are optional, but
// declaring one without its permission is a load error.
type Manifest struct {
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	HookAPI     int      `json:"hookApi"`
	Permissions []string `json:"permissions"` // declared capabilities, audited on load
	Entrypoint  string   `json:"entrypoint"`  // script file, e.g. "index.js"
	MinServer   string   `json:"minServer,omitempty"`
	Description string   `json:"description,omitempty"`

	Runtime   string        `json:"runtime,omitempty"` // goja (default); native/sidecar reserved
	Config    *ConfigSpec   `json:"config,omitempty"`
	Store     *StoreSpec    `json:"store,omitempty"`
	Routes    []RouteSpec   `json:"routes,omitempty"`
	UI        *UISpec       `json:"ui,omitempty"`
	Net       *NetSpec      `json:"net,omitempty"`
	Media     *MediaSpec    `json:"media,omitempty"`
	Requires  *RequiresSpec `json:"requires,omitempty"`
	Conflicts []string      `json:"conflicts,omitempty"`
}

// Permission strings (declarative, audited on load).
const (
	PermReadProfiles  = "profiles:read"
	PermReadTextures  = "textures:read"
	PermEditTextures  = "textures:write"
	PermRegisterRoute = "http:routes" // v1 public GET/HEAD routes
	PermAuthHooks     = "auth:hooks"  // covers join/hasJoined-adjacent hooks

	PermConfig            = "config"
	PermStoreKV           = "store:kv"
	PermStoreDocs         = "store:docs"
	PermUIPage            = "ui:page"
	PermUISlot            = "ui:slot"
	PermUIAssets          = "ui:assets"
	PermUIForms           = "ui:forms"
	PermHTTPRoutesSession = "http:routes:session"
	PermHTTPRoutesAdmin   = "http:routes:admin"
	PermHTTPRoutesSuper   = "http:routes:superadmin"
	PermHTTPRoutesBearer  = "http:routes:bearer"
	PermNetHTTP           = "net:http"
	PermMailSend          = "mail:send"
	PermScheduleJobs      = "schedule:jobs"
	PermCLICommands       = "cli:commands"
	PermPluginsRead       = "plugins:read"
	PermPluginsInvoke     = "plugins:invoke"
	PermAuditWrite        = "audit:write"
	PermEventsSubscribe   = "events:subscribe"
	PermAuthGuards        = "auth:guards"
	PermMediaAvatar       = "media:avatar"
	PermAccountRead       = "account:read"
	PermPlayersRead       = "players:read"
	PermPlayersWrite      = "players:write"
)

// EventPayload is the safe, host-built shape delivered to generic event
// subscribers and guards. It never contains passwords, cookies, or raw
// request bodies.
type EventPayload struct {
	Kind       string         `json:"kind"`
	UserID     int64          `json:"userId,omitempty"`
	PlayerID   int64          `json:"playerId,omitempty"`
	PlayerName string         `json:"playerName,omitempty"`
	UUID       string         `json:"uuid,omitempty"`
	Hash       string         `json:"hash,omitempty"`
	Type       string         `json:"type,omitempty"`
	IP         string         `json:"ip,omitempty"`
	Data       map[string]any `json:"data,omitempty"`
}

// GuardDecision is what a guard callback may return. A deny must carry a
// human-readable message; the host returns 403 without leaking plugin details.
type GuardDecision struct {
	Allow   bool   `json:"allow"`
	Message string `json:"message,omitempty"`
	Code    string `json:"code,omitempty"`
}

// GuardError is returned by Manager.CheckGuard when a guard denies or fails
// closed. Callers map it to 403 and may show Message.
type GuardError struct {
	Plugin  string
	Message string
	Code    string
}

func (e *GuardError) Error() string {
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

// RouteTier is the host middleware applied to a plugin route. The v1
// http:routes permission maps to RouteTierPublic.
type RouteTier string

const (
	RouteTierPublic     RouteTier = "public"
	RouteTierSession    RouteTier = "session"
	RouteTierAdmin      RouteTier = "admin"
	RouteTierSuperAdmin RouteTier = "superadmin"
	RouteTierBearer     RouteTier = "bearer"
)

// BearerResolver maps an Authorization: Bearer credential to a site identity.
// The host wires it to the Yggdrasil token ledger (opaque tokens and OIDC JWTs
// both resolve through ygg.Service). It must never trust a client-supplied uid.
type BearerResolver interface {
	ResolveBearer(ctx context.Context, token string) (*SessionIdentity, error)
}

// BearerError lets the resolver choose the HTTP status/code for a rejected
// token without leaking internals to the route layer.
type BearerError struct {
	Status  int
	Code    string
	Message string
}

func (e *BearerError) Error() string { return e.Code + ": " + e.Message }

// SessionIdentity is the safe subset of a web session exposed to plugins.
type SessionIdentity struct {
	ID         int64  `json:"id"`
	Nickname   string `json:"nickname"`
	Permission int    `json:"permission"`
}

// SessionResolver lets the host resolve a web session without the plugin
// package importing webapi (main wires the store-backed adapter).
type SessionResolver interface {
	ResolveSession(ctx context.Context, token string) (*SessionIdentity, error)
}

// ---- P0 hook contexts (frozen v1; 15-20 hooks, additive from here) ----

// UserProfileFilter mutates a profile payload before it is served
// (CustomSkinAPI / Yggdrasil profile). Filter = value transform.
type UserProfileFilter struct {
	PlayerID    int64
	PlayerName  string
	UUID        string
	TexturesURL map[string]string // "SKIN"/"CAPE" -> url
}

// TextureURLFilter rewrites texture URLs at response time (CDN, mirrors).
type TextureURLFilter struct {
	Hash string
	URL  string
	Type string // "skin" | "cape"
}

// AvatarFilter lets a plugin delegate avatar rendering to an external,
// manifest-allowlisted HTTPS host. URL is empty when the plugin declines.
type AvatarFilter struct {
	Hash   string
	Size   int
	ThreeD bool
	URL    string
}

// TextureUploadedEvent fires after a texture is committed to the store.
type TextureUploadedEvent struct {
	UserID   int64
	PlayerID int64
	Hash     string
	Type     string
}

// UserLifecycleEvent fires for register/login/delete.
type UserLifecycleEvent struct {
	Kind   string // "registered" | "logged_in" | "deleted"
	UserID int64
}

// AuthPhaseEvent fires around Yggdrasil session issuance (fail-open unless
// the hook opts into fail-closed via HostOptions).
type AuthPhaseEvent struct {
	Phase  string // "pre_join" | "post_has_joined"
	UUID   string
	Player string
}

// RouteRegistration lets a plugin contribute read-only HTTP routes
// under /api/plugins/<name>/ (permission: http:routes).
type RouteRegistration struct {
	Method  string
	Path    string
	Handler func(ctx context.Context, req map[string]interface{}) (int, interface{})
}

// HostOptions carries per-hook policy decided by the host. Filters and
// events stay fail-open so a crashing plugin never changes a response status.
type HostOptions struct {
	TimeoutMS int // default 100; join/hasJoined hooks default 50
}

// HookBus is the extension surface plugins program against.
type HookBus interface {
	OnUserProfileFilter(fn func(context.Context, *UserProfileFilter) error, opts HostOptions) error
	OnTextureURLFilter(fn func(context.Context, *TextureURLFilter) error, opts HostOptions) error
	OnTextureUploaded(fn func(context.Context, TextureUploadedEvent) error, opts HostOptions) error
	OnUserLifecycle(fn func(context.Context, UserLifecycleEvent) error, opts HostOptions) error
	OnAuthPhase(fn func(context.Context, AuthPhaseEvent) error, opts HostOptions) error
	OnAvatarFilter(fn func(context.Context, *AvatarFilter) error, opts HostOptions) error
	RegisterRoute(r RouteRegistration) error
}

// Host loads plugins and drives their scripts. The goja implementation lives
// in runtime_goja.go (phase-1 skeleton: interface frozen here).
type Host interface {
	Load(dir string) error
	Bus() HookBus
	Close() error
}
