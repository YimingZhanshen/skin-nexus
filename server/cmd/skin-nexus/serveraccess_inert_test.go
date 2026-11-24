package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// S1's disabled module must be inert by construction, not merely because no
// current request happens to write. Pin the assembly dependency boundary so a
// later store, credential or worker cannot slip into the always-built stack.
func TestServerAccessAssemblyHasNoWriteKeyOrWorkerDeps(t *testing.T) {
	files := []string{filepath.Join("..", "..", "internal", "serveraccess", "config.go"), filepath.Join("..", "..", "internal", "serveraccess", "stack.go"), "serveraccess_boot.go"}
	forbiddenImports := map[string]bool{
		"crypto/rand": true, "database/sql": true,
		"skin-nexus/server/internal/store": true,
		"skin-nexus/server/internal/jobs":  true,
	}
	forbiddenCalls := map[string]bool{
		"SetOption": true, "InsertAudit": true, "GenerateKey": true,
		"StartJanitor": true, "NewTicker": true, "AfterFunc": true,
	}
	for _, path := range files {
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		for _, imp := range f.Imports {
			if forbiddenImports[strings.Trim(imp.Path.Value, `"`)] {
				t.Errorf("%s imports %s: S1 must stay write/key/worker-free", path, imp.Path.Value)
			}
		}
		ast.Inspect(f, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.GoStmt:
				t.Errorf("%s starts a goroutine", path)
			case *ast.CallExpr:
				if sel, ok := node.Fun.(*ast.SelectorExpr); ok && forbiddenCalls[sel.Sel.Name] {
					t.Errorf("%s calls %s: S1 must stay write/key/worker-free", path, sel.Sel.Name)
				}
			}
			return true
		})
	}
}
