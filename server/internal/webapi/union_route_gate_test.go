package webapi

// The route is the feature. A handler written and never registered is invisible
// to every per-layer test: the cache test passes, the union package tests pass,
// the build passes, and the page quietly shows "not available" forever — which
// for the security level is exactly the state a reader cannot tell apart from a
// hub outage. Same class as the dead UnionSync hooks fixed the same day, so the
// same shape of guard applies: read the two files and require them to agree.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// Which of the two passes a scan performs.
const (
	scanDecls = iota // collect handleUnion* method definitions
	scanUses         // collect b.handleUnion* references (the registrations)
)

func unionMethodNames(t *testing.T, file string, mode int) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		switch mode {
		case scanDecls:
			if fd, ok := n.(*ast.FuncDecl); ok && fd.Recv != nil &&
				strings.HasPrefix(fd.Name.Name, "handleUnion") {
				out = append(out, fd.Name.Name)
			}
		case scanUses:
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || !strings.HasPrefix(sel.Sel.Name, "handleUnion") {
				return true
			}
			if id, ok := sel.X.(*ast.Ident); ok && id.Name == "b" {
				out = append(out, sel.Sel.Name)
			}
		}
		return true
	})
	return out
}

func TestEveryUnionHandlerIsRegistered(t *testing.T) {
	defined := unionMethodNames(t, filepath.Join(".", "union_user.go"), scanDecls)
	registered := map[string]bool{}
	for _, name := range unionMethodNames(t, filepath.Join(".", "webapi.go"), scanUses) {
		registered[name] = true
	}
	if len(defined) == 0 {
		t.Fatal("no handleUnion* handlers found in union_user.go — the gate would pass vacuously")
	}
	var missing []string
	for _, name := range defined {
		if !registered[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("handlers defined but never registered, so the endpoint 404s and the UI cannot tell that from an upstream failure: %v", missing)
	}
}
