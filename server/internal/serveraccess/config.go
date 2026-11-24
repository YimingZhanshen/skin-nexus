// Package serveraccess implements the optional server-access module. Slice S1
// only provides an inert health/list surface behind a fail-closed request gate.
package serveraccess

import (
	"time"

	"skin-nexus/server/internal/store"
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
}
