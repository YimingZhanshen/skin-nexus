// Package union implements the MUA Union member-site protocol core (phase 2,
// task H2): hub->member signed inbound endpoints, the member->hub outbound
// client, the Union OAuth2 grant token builder and the hub-distributed
// UnionSigner.
//
// Wire contract is frozen against analysis/yggdrasil-connect.md §4 + §7.1-11
// and analysis/mua-union-official.md (evidence: refs/yggdrasil-connect). Field
// names, header names and concatenation orders are interop-frozen with
// existing MUA Union nodes — do not rename or reorder anything without
// breaking the hub.
//
// The whole module is switchable (phase2 plan §0): when Config.Enabled is
// false the constructors return inert objects — Enabled() reports false,
// inbound handlers answer 404 and outbound calls fail fast with ErrDisabled.
// Whether routes are mounted at all (the real "zero surface" guarantee) is
// decided by the caller in cmd/skin-nexus.
package union

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

// QueryEmailMode values (Config.QueryEmailMode). Anything else is rejected by
// Config.Validate and treated as off by the handlers (fail closed).
const (
	QueryEmailModeFull = "full" // answer 200 {"email":...} / 204 via QueryEmailFunc
	QueryEmailModeOff  = "off"  // refuse with 403
)

// Inbound request headers of the hub->member signed channel
// (analysis/yggdrasil-connect.md §4.3, byte-exact names) plus the outbound
// member auth header (§4.2).
const (
	HeaderMessageSignature = "X-Message-Signature"
	HeaderMessageTimestamp = "X-Message-Timestamp"
	HeaderMessageNonce     = "X-Message-Nonce"
	HeaderMemberKey        = "X-Union-Member-Key"
)

// Error-body codes/values. Union error bodies keep the legacy
// {error,errorMessage,cause?} shape (analysis/yggdrasil-connect.md §7.1-7);
// the 403 code and message match the plugin's ForbiddenOperationException so
// existing hub error handling keeps working.
const (
	codeForbidden    = "ForbiddenOperationException"
	codeIllegalArg   = "IllegalArgumentException"
	codeUpstream     = "UnionUpstreamError"
	codeNotSupported = "NotImplementedException"
	verifyFailureMsg = "Union host verification failure."
)

// Signature verification policy (§4.3): timestamp window and nonce lifetime.
const (
	VerifyPastSkew   = 10 * time.Second // timestamp >= now-10s
	VerifyFutureSkew = 30 * time.Second // timestamp <= now+30s
	NonceTTL         = 60 * time.Second // replay cache lifetime
)

// AuditFunc receives security-relevant events as (event, detail) pairs. A nil
// AuditFunc is a no-op; every failure path calls it (phase2 plan §6 audit).
type AuditFunc func(event string, detail map[string]any)

// call is a nil-safe invocation helper.
func (f AuditFunc) call(event string, detail map[string]any) {
	if f != nil {
		f(event, detail)
	}
}

// Config is the deployment configuration of the Union member module. Field
// names follow docs/phase2-dev-plan.md §6 (SKIN_UNION_* env mapping is done by
// the caller in internal/config).
type Config struct {
	// Enabled switches the whole module. False => inert objects, zero surface.
	Enabled bool

	// ApiRoot is the hub API root, e.g. "https://skin.mualliance.ltd/api/union"
	// (option union_api_root). Trailing slashes are ignored.
	ApiRoot string

	// MemberKey is the shared member token (option union_member_key), sent as
	// X-Union-Member-Key on every outbound call and used as the HMAC key of the
	// OAuth2 grant token. Rotatable via the updatebackendkey endpoint.
	MemberKey string

	// HostPublicKeyPEM is the *pinned* hub message-signing public key (PKIX
	// PEM, PKCS#1 accepted too). Phase-2 security decision (plan §5/§7.2): the
	// legacy plugin re-fetches it from the hub on every request, which makes
	// the whole trust chain depend on the transport; we pin it instead.
	HostPublicKeyPEM string

	// AllowUpdateBackendKey enables the member-key rotation endpoint. Default
	// false: a remote party must not be able to rewrite the shared secret
	// (analysis/yggdrasil-connect.md §7.2).
	AllowUpdateBackendKey bool

	// QueryEmailMode is QueryEmailModeFull or QueryEmailModeOff (fail closed).
	QueryEmailMode string

	// HubOrigin is the hub origin ("scheme://host") allowed by CORS on the
	// public OAuth2 signature-key endpoint. When empty it is derived from
	// ApiRoot (legacy behavior).
	HubOrigin string

	// SigPrivateKeyPEM / SigPublicKeyPEM are this site's Union OAuth2
	// user-info signing pair (option union_oauth2_sig_private_key/public_key,
	// PKCS#8/PKCS#1 private, PKIX public). SigPublicKeyPEM may be empty: it is
	// then derived from the private key.
	SigPrivateKeyPEM string
	SigPublicKeyPEM  string

	// YggdrasilApiVersion is reported as "yggdrasilApiVersion" in the member
	// self-description (the legacy value is the plugin version).
	YggdrasilApiVersion string

	// Optional enabledFeatures toggles for the self-description
	// (analysis/yggdrasil-connect.md §4.4). "unionBlacklist" is always listed,
	// matching the legacy plugin.
	//
	// These two are live reads, not boot-time booleans: the reference asks
	// option() on every hello (UnionController.php:19-20), and the hub uses the
	// list to decide what it may ask this member site to do. nil = not
	// advertised. EmailVerification mirrors the site's require_verification join
	// gate; InvitationCodesForUnion mirrors the invite-code gate — the legacy
	// option it reads is written true when the invitation-codes plugin is
	// enabled and false when it is disabled (MUAlliance/invitation-codes
	// callbacks.php:23-28), so "registration here needs a code" is its meaning.
	EmailVerification       func(ctx context.Context) bool
	InvitationCodesForUnion func(ctx context.Context) bool
	UnionOAuth2             bool

	// RequestTimeout bounds a single outbound hub call (legacy: 5s; diagnose
	// uses 10s). Zero means the default.
	RequestTimeout time.Duration
}

// Errors returned across the package.
var (
	// ErrDisabled is returned by every action of an inert (Config.Enabled ==
	// false) object.
	ErrDisabled = errors.New("union: module is disabled")
	// ErrNotConfigured marks a missing required callback or credential.
	ErrNotConfigured = errors.New("union: not configured")
	// ErrRotationSuspended is returned by UnionSigner rotation after the daily
	// change budget has been exceeded (human review required).
	ErrRotationSuspended = errors.New("union: automatic key rotation suspended")
	// ErrNoSigningKey is returned when neither a hub key nor a fallback signer
	// is available.
	ErrNoSigningKey = errors.New("union: no signing key available")
)

// defaults
const (
	defaultRequestTimeout = 5 * time.Second
	diagnoseTimeout       = 10 * time.Second // legacy triggerDiagnose timeout
)

// Validate reports obviously broken configuration. It is lenient by design:
// a missing MemberKey is legal at bootstrap (the hub issues it later) and an
// empty QueryEmailMode behaves like QueryEmailModeOff.
func (c Config) Validate() error {
	if !c.Enabled {
		return nil
	}
	root := strings.TrimSpace(c.ApiRoot)
	if root == "" {
		return errors.New("union: ApiRoot is required when enabled")
	}
	u, err := url.Parse(root)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return errors.New("union: ApiRoot must be an absolute URL")
	}
	switch c.QueryEmailMode {
	case "", QueryEmailModeFull, QueryEmailModeOff:
	default:
		return errors.New("union: QueryEmailMode must be \"full\" or \"off\"")
	}
	return nil
}

// requestTimeout resolves the per-call timeout.
func (c Config) requestTimeout() time.Duration {
	if c.RequestTimeout > 0 {
		return c.RequestTimeout
	}
	return defaultRequestTimeout
}

// hubOrigin resolves the CORS origin of the hub: explicit config first, then
// scheme://host of ApiRoot (legacy UnionOAuth2Controller derives it that way).
func (c Config) hubOrigin() string {
	if o := strings.TrimSpace(c.HubOrigin); o != "" {
		return strings.TrimRight(o, "/")
	}
	u, err := url.Parse(strings.TrimSpace(c.ApiRoot))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// apiRoot returns the trimmed hub root for URL building.
func (c Config) apiRoot() string {
	return strings.TrimRight(strings.TrimSpace(c.ApiRoot), "/")
}

// queryEmailFull reports whether the queryemail endpoint may disclose emails.
// Unknown modes fail closed.
func (c Config) queryEmailFull() bool {
	return c.Enabled && c.QueryEmailMode == QueryEmailModeFull
}
