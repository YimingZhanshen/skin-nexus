package adminapi

// authgate_test.go — every route this package registers must be wrapped in
// b.auth, or be named on the exemption list.
//
// Why a gate: the admin surface is one mux with one authentication wrapper,
// and the wrapper is the only thing standing between an unauthenticated
// browser and a super-admin action. A route added without it compiles, routes
// perfectly, and answers — that is exactly how three theme routes shipped
// unwrapped once (commit d3d0d5c fixed them, no test noticed). This check
// reads the registration calls themselves, so the mistake is caught where it
// is made rather than by whoever eventually clicks the new button.
//
// The exemption list is for routes that are deliberately public (a health
// probe, an unauthenticated self-check). It starts empty: today every route
// in this package wants the wrapper.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

// unauthenticated lists route registrations that intentionally skip b.auth.
// Each entry is the exact mux pattern string.
var unauthenticated = []string{}

func TestEveryAdminRouteIsAuthenticated(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	exempt := map[string]bool{}
	for _, p := range unauthenticated {
		exempt[p] = true
	}

	var bare []string
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) < 2 {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "mux" || (sel.Sel.Name != "Handle" && sel.Sel.Name != "HandleFunc") {
				return true
			}
			pattern, ok := stringLit(call.Args[0])
			if !ok {
				return true // a non-literal pattern cannot be checked here
			}
			if exempt[pattern] {
				return true
			}
			handler := call.Args[1]
			wrapped, isAuth := handler.(*ast.CallExpr)
			if isAuth {
				if s, ok := wrapped.Fun.(*ast.SelectorExpr); ok {
					if id, ok := s.X.(*ast.Ident); ok {
						isAuth = id.Name == "b" && s.Sel.Name == "auth"
					} else {
						isAuth = false
					}
				} else {
					isAuth = false
				}
			}
			if !isAuth {
				pos := fset.Position(call.Pos())
				bare = append(bare, name+":"+itoa(pos.Line)+" "+pattern)
			}
			return true
		})
	}
	if len(bare) > 0 {
		t.Fatalf("route(s) registered without b.auth — the admin surface must be authenticated at the mux:\n  %s",
			strings.Join(bare, "\n  "))
	}
}

func stringLit(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s := lit.Value
	if len(s) >= 2 && s[0] == '"' {
		return s[1 : len(s)-1], true
	}
	return "", false
}
