package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// S2 assembly may hold a store reference, but construction and the disabled
// gate still must not generate keys, write options/audit or start workers.
func TestServerAccessAssemblyHasNoWriteKeyOrWorkerDeps(t *testing.T) {
	files := []string{filepath.Join("..", "..", "internal", "serveraccess", "config.go"), filepath.Join("..", "..", "internal", "serveraccess", "stack.go"), "serveraccess_boot.go"}
	forbiddenImports := map[string]bool{
		"crypto/rand": true, "database/sql": true,
		"skin-nexus/server/internal/jobs": true,
	}
	forbiddenCalls := map[string]bool{
		"SetOption": true, "InsertAudit": true, "GenerateKey": true,
		"StartJanitor": true, "NewTicker": true, "AfterFunc": true,
	}
	forbiddenBootCalls := map[string]bool{"SetOption": true, "InsertAudit": true}
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
				if sel, ok := node.Fun.(*ast.SelectorExpr); ok {
					if forbiddenCalls[sel.Sel.Name] || path == "serveraccess_boot.go" && forbiddenBootCalls[sel.Sel.Name] {
						t.Errorf("%s calls %s: assembly must stay write/key/worker-free", path, sel.Sel.Name)
					}
				}
			}
			return true
		})
	}
}
