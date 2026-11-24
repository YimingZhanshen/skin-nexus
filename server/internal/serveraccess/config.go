// Package serveraccess implements the optional server-access module. Slice S1
// only provides an inert health/list surface behind a fail-closed request gate.
package serveraccess

// Config is resolved by the caller from the serveraccess_enabled option first,
// then SKIN_SERVERACCESS_ENABLED. The zero value is deliberately disabled.
type Config struct {
	Enabled bool
}
