// Package account is the shared, uid-scoped character/texture service used by
// the browser API and the plugin account bridge. Every mutation validates
// ownership, runs the same guards/events/audit, and never trusts a caller-
// supplied uid.
package account

import (
	"context"
	"fmt"

	"skin-nexus/server/internal/plugin"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/texture"
)

// Error is a stable, transport-neutral failure. Web handlers map Code to HTTP
// statuses and show Message; the launcher plugin maps Code to its own JSON.
type Error struct {
	Code    string
	Message string
	Err     error
	Details map[string]any
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

const (
	CodeNotFound      = "player.not_found"
	CodeLimit         = "player.limit"
	CodeInvalidName   = "player.name_invalid"
	CodeNameTaken     = "player.name_taken"
	CodeInvalid       = "texture.invalid"
	CodeTooLarge      = "texture.too_large"
	CodeNotOwned      = "texture.not_owned"
	CodeTypeMismatch  = "texture.type_mismatch"
	CodeLibraryNotFnd = "library.not_found"
	CodeForbidden     = "account.forbidden"
	CodeDuplicatePub  = "texture.duplicate_public"
	CodeSourceURLReq  = "texture.source_url_required"
	CodeSourceURLBad  = "texture.source_url_invalid"
	CodeInternal      = "internal"
)

func errf(code, msg string, err error) *Error {
	return &Error{Code: code, Message: msg, Err: err}
}

func errDetails(code, msg string, details map[string]any) *Error {
	return &Error{Code: code, Message: msg, Details: details}
}

// CallMeta carries request context that must not come from plugin JavaScript.
// The host adapter fills Plugin/Version/Signer from the loaded manifest.
type CallMeta struct {
	IP         string
	Filename   string
	PluginData map[string]any
	Plugin     string
	Version    string
	Signer     string
}

func (m CallMeta) via() string {
	if m.Plugin != "" {
		return "plugin"
	}
	return "web"
}

// UploadMeta is the user-editable metadata accepted on upload/import.
type UploadMeta struct {
	Name      string
	DetailMD  string
	Source    string
	SourceURL string
	Public    bool
}

// Player is the neutral row shape shared by both transports.
type Player struct {
	ID        int64
	UID       int64
	Name      string
	UUID      string
	SkinTID   int64
	CapeTID   int64
	SkinHash  string
	CapeHash  string
	SkinModel string
}

// TextureView is the launcher-facing texture projection.
type TextureView struct {
	ID          int64  `json:"textureId"`
	Type        string `json:"type"`
	Hash        string `json:"hash"`
	Model       string `json:"model,omitempty"`
	Description string `json:"description,omitempty"`
	DetailMD    string `json:"detail,omitempty"`
	Source      string `json:"source,omitempty"`
	SourceURL   string `json:"sourceUrl,omitempty"`
	URL         string `json:"url"`
	PreviewURL  string `json:"previewUrl,omitempty"`
	AvatarURL   string `json:"avatarUrl,omitempty"`
	UploadedAt  string `json:"uploadedAt,omitempty"`
	Bytes       int64  `json:"bytes,omitempty"`
}

type PlayerView struct {
	ID   int64        `json:"id"`
	Name string       `json:"name"`
	UUID string       `json:"uuid"`
	Skin *TextureView `json:"skin"`
	Cape *TextureView `json:"cape"`
}

type AccountView struct {
	ID           int64  `json:"id"`
	Nickname     string `json:"nickname"`
	Permission   int    `json:"permission"`
	PlayerLimit  int    `json:"playerLimit"`
	PlayerCount  int    `json:"playerCount"`
	AvatarURL    string `json:"avatarUrl"`
	StorageBytes int64  `json:"storageBytes"`
}

type SiteLimits struct {
	MaxUploadBytes int64 `json:"maxUploadBytes"`
	MaxPlayers     int   `json:"maxPlayers"`
	OIDCEnabled    bool  `json:"oidc"`
}

type LibraryItem struct {
	TID        int64  `json:"tid"`
	Name       string `json:"name"`
	Source     string `json:"source,omitempty"`
	SourceURL  string `json:"sourceUrl,omitempty"`
	DetailMD   string `json:"detail,omitempty"`
	Type       string `json:"type"`
	Model      string `json:"model"`
	Hash       string `json:"hash"`
	Uploader   string `json:"uploader"`
	Likes      int64  `json:"likes"`
	Liked      bool   `json:"liked"`
	Public     bool   `json:"public"`
	Owned      bool   `json:"owned"`
	TextureURL string `json:"textureUrl"`
	PreviewURL string `json:"previewUrl,omitempty"`
	AvatarURL  string `json:"avatarUrl,omitempty"`
	UploadedAt string `json:"uploadedAt,omitempty"`
}

type LibraryPage struct {
	Items   []LibraryItem `json:"items"`
	Total   int           `json:"total"`
	Page    int           `json:"page"`
	PerPage int           `json:"perPage"`
}

// Service owns all character/texture mutations. Callers pass the authenticated
// uid explicitly; the plugin bridge takes it from the route identity.
type Service struct {
	Store             *store.Mysql
	Textures          *texture.Store
	MaxChars          int
	BaseURL           string
	OIDCEnabled       bool
	CheckGuard        func(ctx context.Context, kind string, p plugin.EventPayload) error
	EmitEvent         func(ctx context.Context, kind string, p plugin.EventPayload)
	OnTextureUploaded func(ctx context.Context, e plugin.TextureUploadedEvent)
	Audit             func(ctx context.Context, uid int64, action string, detail map[string]any)
	UnionSync         func(ctx context.Context, uuid, name string)
}

func (s *Service) ensure() *Error {
	if s == nil || s.Store == nil {
		return errf(CodeInternal, "账号服务不可用", nil)
	}
	return nil
}

func (s *Service) base() string {
	return trimRight(s.BaseURL)
}

func trimRight(v string) string {
	for len(v) > 0 && v[len(v)-1] == '/' {
		v = v[:len(v)-1]
	}
	return v
}

func (s *Service) textureURL(hash string) string { return s.base() + "/textures/" + hash }
func (s *Service) previewURL(tid int64, typ string) string {
	if typ != "skin" {
		return ""
	}
	return fmt.Sprintf("%s/preview/%d", s.base(), tid)
}
func (s *Service) avatarURL(hash string) string {
	if hash == "" {
		return ""
	}
	return s.base() + "/avatar/hash/" + hash
}
