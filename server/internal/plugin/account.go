package plugin

import "context"

// AccountView is the launcher-facing account projection. No email/uid leaks.
type AccountView struct {
	ID           int64  `json:"id"`
	Nickname     string `json:"nickname"`
	Permission   int    `json:"permission"`
	PlayerLimit  int    `json:"playerLimit"`
	PlayerCount  int    `json:"playerCount"`
	AvatarURL    string `json:"avatarUrl"`
	StorageBytes int64  `json:"storageBytes"`
}

type PlayerView struct {
	ID   int64        `json:"id"`
	Name string       `json:"name"`
	UUID string       `json:"uuid"`
	Skin *TextureView `json:"skin"`
	Cape *TextureView `json:"cape"`
}

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

type LibraryQuery struct {
	Type    string
	Keyword string
	Sort    string
	Page    int
	PerPage int
}

type LibraryItem struct {
	TID        int64  `json:"tid"`
	Name       string `json:"name"`
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

// AccountCallMeta is request context supplied by the host. Plugin JavaScript
// can only provide the filename/pluginData values; identity fields are
// overwritten by the runtime bridge.
// UploadMeta is the optional fifth argument for texture upload/import.
type UploadMeta struct {
	Name      string
	DetailMD  string
	Source    string
	SourceURL string
	Public    bool
}

type AccountCallMeta struct {
	IP         string
	Filename   string
	PluginData map[string]any
	Plugin     string
	Version    string
	Signer     string
}

// AccountError is a stable failure surfaced to JS as {code,message}.
type AccountError struct {
	Code    string
	Message string
	Details map[string]any
}

func (e *AccountError) Error() string { return e.Code + ": " + e.Message }

// AccountService is implemented in cmd/skin-nexus on top of internal/account.
// All methods take the caller uid from the route identity, never from JS.
type AccountService interface {
	Account(ctx context.Context, uid int64) (AccountView, error)
	Limits(ctx context.Context) (SiteLimits, error)
	ListPlayers(ctx context.Context, uid int64) ([]PlayerView, error)
	GetPlayer(ctx context.Context, uid, pid int64) (PlayerView, error)
	CreatePlayer(ctx context.Context, uid int64, name string, meta AccountCallMeta) (PlayerView, bool, error)
	RenamePlayer(ctx context.Context, uid, pid int64, name string, meta AccountCallMeta) (PlayerView, bool, error)
	DeletePlayer(ctx context.Context, uid, pid int64, meta AccountCallMeta) error
	SetTexture(ctx context.Context, uid, pid int64, kind string, tid int64, meta AccountCallMeta) (PlayerView, error)
	ListTextures(ctx context.Context, uid int64) ([]TextureView, error)
	UploadTexture(ctx context.Context, uid int64, typ, model, desc string, png []byte, meta AccountCallMeta) (TextureView, bool, error)
	UploadTextureMeta(ctx context.Context, uid int64, typ, model string, um UploadMeta, png []byte, meta AccountCallMeta) (TextureView, bool, error)
	ImportPrivateTexture(ctx context.Context, uid int64, typ, model, desc string, png []byte, meta AccountCallMeta) (TextureView, bool, error)
	ListLibrary(ctx context.Context, uid int64, q LibraryQuery) (LibraryPage, error)
	GetLibrary(ctx context.Context, uid, tid int64) (LibraryItem, error)
	ImportLibrary(ctx context.Context, uid, tid int64, meta AccountCallMeta) (TextureView, bool, error)
}

type SiteLimits struct {
	MaxUploadBytes int64 `json:"maxUploadBytes"`
	MaxPlayers     int   `json:"maxPlayers"`
	OIDCEnabled    bool  `json:"oidc"`
}
