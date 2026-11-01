package adminapi

// errorgate_test.go puts the management surface under the same envelope gate as
// the player surface: `code` is what a language pack can translate, and prose
// hard-coded in a handler is what an English panel cannot render. The engine is
// internal/errorgate; this file only says which functions here emit responses.

import (
	"go/ast"
	"testing"

	"skin-nexus/server/internal/errorgate"
)

const budgetFile = "testdata/error-budget.txt"

var codeOwners = []string{"../i18n", "../account"}

var governed = []errorgate.Scope{
	{
		Name: "adminapi",
		Dir:  ".",
		// The envelope helpers in errors.go *are* the envelope; their bodies are
		// not call sites. Declaring them as emitters below is what makes every
		// response in this package checked once, at the place it is written.
		Impl: []string{"writeJSON", "writeErrCode", "writeErrBody", "writeDomain", "writeInternal", "writeLogged", "writeStoreErr", "writeAccountErr"},
		Coded: []errorgate.Emitter{
			{Name: "writeErrCode", Prose: 2, Code: 3},
			{Name: "writeErrBody", Prose: 3, Code: 2},
			{Name: "writeDomain", Prose: 2, Code: 3},
			{Name: "writeLogged", Prose: 4, Code: 3},
		},
		// Every legacy site is a hand-rolled writeJSON body, which is exactly
		// what Body watches: a map with an "error" key and no code.
		Body:       "writeJSON",
		CodeOwners: codeOwners,
	},
}

func TestErrorBudget(t *testing.T) {
	errorgate.Budget(t, budgetFile, governed...)
}

func TestCodesComeFromCatalogue(t *testing.T) {
	errorgate.CheckCodes(t, governed...)
}

// TestNoRawErrorInBody is the leak check. `err.Error()` on an admin route is a
// Go error — a driver string, a SQL fragment, a path — and the panel is the one
// surface an attacker with a stolen session can poke at. The operator still has
// to see it, so it goes to the log instead of the response.
//
// A hand-rolled body is inspected whole, so the leak is caught whichever key it
// hides under.
func TestNoRawErrorInBody(t *testing.T) {
	// The declared-domain forms (writeDomain, writeErrBody) take an `error` and
	// are reached through it, so nothing they emit can be mistaken for a leak.
	// Everywhere else, `.Error()` reaching a body is the finding.
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
