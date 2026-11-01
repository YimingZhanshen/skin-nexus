package webapi

// errorgate_test.go is the envelope gate for the player-facing HTTP surface.
// The machinery lives in internal/errorgate; what is declared here is which
// functions in these packages emit a response, and which argument of each holds
// the prose and the code.
//
// The account service is watched from here because its prose reaches the browser
// through writeAccountErr — a second gate elsewhere would only duplicate the
// measurement without watching a different surface.

import (
	"go/ast"
	"testing"

	"skin-nexus/server/internal/errorgate"
)

var codeOwners = []string{"../i18n", "../account"}

var governed = []errorgate.Scope{
	{
		Name: "webapi",
		Dir:  ".",
		Impl: []string{"writeJSON", "writeErrCode", "writeErrBody", "writeDomain", "writeAccountErr",
			"writeLogged", "writeInternal"},
		Coded: []errorgate.Emitter{
			{Name: "writeErrCode", Prose: 2, Code: 3},
			{Name: "writeDomain", Prose: 2, Code: 3},
			{Name: "writeErrBody", Prose: 3, Code: 2},
			{Name: "writeLogged", Prose: 4, Code: 3},
			{Name: "writeAccountErr", Prose: 1, Code: -1},
		},
		Body:       "writeJSON",
		CodeOwners: codeOwners,
	},
	{
		Name: "account",
		Dir:  "../account",
		Impl: []string{"errf", "errDetails", "errDomain"},
		// Every account error is built through errf/errDetails, so a call that
		// leaves the code argument out would be the uncoded case; the emitters
		// take it mandatorily, so none is declared below.
		Uncoded: nil,
		Coded: []errorgate.Emitter{
			{Name: "errf", Prose: 1, Code: 0},
			{Name: "errDetails", Prose: 1, Code: 0},
			{Name: "errDomain", Prose: 1, Code: 0},
		},
		CodeOwners: codeOwners,
	},
}

func TestErrorEnvelopeIsClean(t *testing.T) {
	errorgate.AssertClean(t, governed...)
}

func TestCodesComeFromCatalogue(t *testing.T) {
	errorgate.CheckCodes(t, governed...)
}

// TestNoRawErrorInBody is the leak check, identical in intent to the management
// surface's: a Go error reaching a browser is a driver string or a path, and the
// only way to put one in a body is to declare it a message written for the
// caller (writeDomain).
func TestNoRawErrorInBody(t *testing.T) {
	for _, sc := range governed {
		for _, s := range errorgate.Sites(t, sc) {
			if leaky(s.Prose) {
				t.Errorf("%s: an internal error is being written to the response body; log it and answer with a code", s.Pos)
			}
		}
	}
}

// leaky reports whether an expression calls a method named Error — the shape of
// a Go error reaching a JSON body.
func leaky(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && errorgate.CallName(call) == "Error" {
			found = true
		}
		return true
	})
	return found
}
