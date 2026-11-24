package store

import (
	"database/sql"
	"errors"
	"time"
)

var (
	ErrLastOwner = errors.New("store: last tenant owner")
	ErrQuota     = errors.New("store: server access quota exceeded")
)

type SATenant struct {
	ID          int64        `json:"id"`
	Name        string       `json:"name"`
	Status      string       `json:"status"`
	ServerLimit int          `json:"serverLimit"`
	CreatedAt   time.Time    `json:"createdAt"`
	DeletedAt   sql.NullTime `json:"deletedAt,omitempty"`
}

type SATenantMember struct {
	TenantID  int64     `json:"tenantId"`
	UID       int64     `json:"uid"`
	Role      string    `json:"role"`
	CreatedAt time.Time `json:"createdAt"`
	Nickname  string    `json:"nickname"`
}

type SATenantInvite struct {
	ID         int64        `json:"id"`
	TenantID   int64        `json:"tenantId"`
	Email      string       `json:"email"`
	Role       string       `json:"role"`
	ExpiresAt  time.Time    `json:"expiresAt"`
	AcceptedAt sql.NullTime `json:"acceptedAt,omitempty"`
	CreatedAt  time.Time    `json:"createdAt"`
}

type SAServer struct {
	ID         int64     `json:"id"`
	TenantID   int64     `json:"-"`
	PublicID   string    `json:"publicId"`
	Name       string    `json:"name"`
	Status     string    `json:"status"`
	SourceMode string    `json:"sourceMode"`
	CreatedAt  time.Time `json:"createdAt"`
}

type SACredential struct {
	ID         int64        `json:"id"`
	TenantID   int64        `json:"-"`
	ServerID   int64        `json:"serverId"`
	Mode       string       `json:"mode"`
	SecretHash string       `json:"-"`
	Scopes     string       `json:"scopes"`
	Status     string       `json:"status"`
	ExpiresAt  sql.NullTime `json:"expiresAt,omitempty"`
	LastUsedAt sql.NullTime `json:"lastUsedAt,omitempty"`
	CreatedAt  time.Time    `json:"createdAt"`
}

type SAServerPolicy struct {
	ServerID               int64 `json:"serverId"`
	WhitelistEnabled       bool  `json:"whitelistEnabled"`
	PremiumFallbackEnabled bool  `json:"premiumFallbackEnabled"`
	VerifyRateLimit        int   `json:"verifyRateLimit"`
	VerifyBurst            int   `json:"verifyBurst"`
	MaxConcurrent          int   `json:"maxConcurrent"`
	WhitelistLimit         int   `json:"whitelistLimit"`
	EventRetentionDays     int   `json:"eventRetentionDays"`
}

type SAServerWhitelist struct {
	ID        int64     `json:"id"`
	ServerID  int64     `json:"-"`
	UID       int64     `json:"uid"`
	Note      string    `json:"note"`
	CreatedBy int64     `json:"createdBy"`
	CreatedAt time.Time `json:"createdAt"`
}

type SAServerBan struct {
	ID        int64        `json:"id"`
	ServerID  int64        `json:"-"`
	UID       int64        `json:"uid"`
	Reason    string       `json:"reason"`
	ExpiresAt sql.NullTime `json:"expiresAt,omitempty"`
	CreatedBy int64        `json:"createdBy"`
	CreatedAt time.Time    `json:"createdAt"`
}

type SAJoinEvent struct {
	ID            int64         `json:"id"`
	TenantID      int64         `json:"-"`
	ServerID      int64         `json:"-"`
	JoinSessionID sql.NullInt64 `json:"joinSessionId,omitempty"`
	UID           sql.NullInt64 `json:"uid,omitempty"`
	UUID          string        `json:"uuid"`
	PlayerName    string        `json:"playerName"`
	Outcome       string        `json:"outcome"`
	ReasonCode    string        `json:"reasonCode"`
	SourceMode    string        `json:"sourceMode"`
	DurationMS    int64         `json:"durationMs"`
	RequestID     string        `json:"requestId"`
	CreatedAt     time.Time     `json:"createdAt"`
}

// SAPlayerIdentity is the complete owner-facing result of a player lookup.
// Texture, profile, email and permission data never cross this boundary.
type SAPlayerIdentity struct {
	UID  int64  `json:"uid"`
	Name string `json:"name"`
	UUID string `json:"uuid"`
}

// SAOwnerJoinEvent is the allowlisted timeline projection. Keeping a separate
// type prevents a future store field from becoming an accidental owner API leak.
type SAOwnerJoinEvent struct {
	ID         int64     `json:"id"`
	CreatedAt  time.Time `json:"createdAt"`
	Outcome    string    `json:"outcome"`
	ReasonCode string    `json:"reasonCode"`
	DurationMS int64     `json:"durationMs"`
	SourceMode string    `json:"sourceMode"`
}

// TrustLevel derives the S3 trust label from the persisted source mode. Both
// supported modes prove possession of a credential only; neither is attested.
func (e SAJoinEvent) TrustLevel() string {
	switch e.SourceMode {
	case "url_secret":
		return "possessed_credential"
	case "header":
		return "header_credential"
	default:
		return "untrusted"
	}
}
