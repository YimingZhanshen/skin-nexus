// Package serveraccess implements the optional server-access module. Slice S1
// only provides an inert health/list surface behind a fail-closed request gate.
package serveraccess

import (
	"context"
	"time"

	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/ygg"
)

// Config is resolved by the caller from the serveraccess_enabled option first,
// then SKIN_SERVERACCESS_ENABLED. The zero value is deliberately disabled.
type Config struct {
	Enabled bool
	Store   *store.Mysql
	Now     func() time.Time
	// InviteTTL defaults to 72h, credential lifetime to 90d and rotation overlap
	// to 24h. Boot leaves these at zero; no key material is generated at assembly.
	InviteTTL       time.Duration
	CredentialTTL   time.Duration
	RotationOverlap time.Duration
	// Verifier is the narrow Yggdrasil extension used only by S3 routes.
	Verifier *ygg.API
	// OnEventError is an additional metric/alert hook. Event persistence is
	// always best-effort and failures are also emitted as structured logs.
	OnEventError func(context.Context, store.SAJoinEvent, error)
}
