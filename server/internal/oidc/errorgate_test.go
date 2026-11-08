package oidc

// errorgate_test.go puts the OIDC / device-code surface under the same envelope
// gate the player-facing API has been under since the code catalogue landed.
// The device flow is a browser-facing surface (the consent screen is a page, and
// a launcher polls the token endpoint), so prose written here is copy an English
// UI cannot render — and `{"error": "拒绝失败"}` is doubly wrong: it is Chinese,
// and it is not an OAuth error code anyone can branch on.

import (
	"testing"

	"skin-nexus/server/internal/errorgate"
)

var governed = []errorgate.Scope{
	{
		Name: "oidc",
		Dir:  ".",
		Impl: []string{"writeJSON", "writeErrStatus"},
		Coded: []errorgate.Emitter{
			{Name: "writeErrStatus", Prose: 2, Code: 3},
			{Name: "errEnvelope", Prose: 1, Code: 0},
		},
		Body:       "writeJSON",
		CodeOwners: []string{"../i18n"},
	},
}

func TestErrorEnvelopeIsClean(t *testing.T) {
	errorgate.AssertClean(t, governed...)
}

func TestCodesComeFromCatalogue(t *testing.T) {
	errorgate.CheckCodes(t, governed...)
}
