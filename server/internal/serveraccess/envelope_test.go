package serveraccess

import (
	"testing"

	"skin-nexus/server/internal/errorgate"
)

func TestServerAccessErrorEnvelopeUsesDeclaredCodes(t *testing.T) {
	scope := errorgate.Scope{
		Name: "serveraccess", Dir: ".",
		Impl:       []string{"writeSAError", "writeSAStoreError"},
		Coded:      []errorgate.Emitter{{Name: "writeSAError", Prose: 3, Code: 2}},
		CodeOwners: []string{"../i18n"},
	}
	errorgate.AssertClean(t, scope)
	errorgate.CheckCodes(t, scope)
}
