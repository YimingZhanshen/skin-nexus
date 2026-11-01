//go:build integration

// engine_gate.go: the escape hatch for the handful of integration tests that
// only mean something on one engine. The portable suite runs against MySQL,
// MariaDB, PostgreSQL and SQLite alike, and a MySQL-only fixture must not read
// as a silent pass on the others — a skip that names what it needed is the
// honest answer.
package store

import (
	"testing"

	"skin-nexus/server/internal/dialect"
)

func requireEngines(t *testing.T, m *Mysql, want ...dialect.Engine) {
	t.Helper()
	for _, e := range want {
		if m.Engine() == e {
			return
		}
	}
	t.Skipf("%s needs %v, this database is %s", t.Name(), want, m.Engine())
}
