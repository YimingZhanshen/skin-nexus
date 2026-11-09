package main

// A seam nobody calls is worse than no seam: `Outbound.SyncProfileRename` and
// `SyncProfileDelete` both existed, both were unit-tested against a recording
// hub, and neither had a caller — so a player renamed or deleted here stayed
// published on the hub forever, and nothing failed. The declaration, the call
// site and the wiring are three different files, and only their agreement is
// the feature. This gate reads all three and requires the same set of names in
// each, which is the part a per-layer test structurally cannot see.
import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// declaredUnionHooks returns the Union* fields on account.Service.
func declaredUnionHooks(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join("..", "..", "internal", "account", "service.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse account/service.go: %v", err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		ts, ok := n.(*ast.TypeSpec)
		if !ok || ts.Name.Name != "Service" {
			return true
		}
		st, ok := ts.Type.(*ast.StructType)
		if !ok {
			return true
		}
		for _, fld := range st.Fields.List {
			for _, name := range fld.Names {
				if strings.HasPrefix(name.Name, "Union") {
					out = append(out, name.Name)
				}
			}
		}
		return true
	})
	return out
}

// calledUnionHooks returns the Union* fields account.Service actually invokes on
// itself (s.UnionX), which is where a hook goes to be dead.
func calledUnionHooks(t *testing.T) []string {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filepath.Join("..", "..", "internal", "account", "players.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse account/players.go: %v", err)
	}
	var out []string
	ast.Inspect(f, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok && id.Name == "s" && strings.HasPrefix(sel.Sel.Name, "Union") {
			out = append(out, sel.Sel.Name)
		}
		return true
	})
	return out
}

// wiredUnionHooks returns the Union* keys the composition root sets on the one
// account.Service literal it builds.
func wiredUnionHooks(t *testing.T) []string {
	t.Helper()
	var out []string
	err := filepath.Walk(".", func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			return nil
		}
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.CompositeLit)
			if !ok {
				return true
			}
			sel, ok := lit.Type.(*ast.SelectorExpr)
			if !ok || sel.Sel.Name != "Service" {
				return true
			}
			if pkg, ok := sel.X.(*ast.Ident); !ok || pkg.Name != "account" {
				return true
			}
			for _, e := range lit.Elts {
				kv, ok := e.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if k, ok := kv.Key.(*ast.Ident); ok && strings.HasPrefix(k.Name, "Union") {
					out = append(out, k.Name)
				}
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatalf("walk cmd/skin-nexus: %v", err)
	}
	return out
}

func TestEveryUnionHookIsDeclaredCalledAndWired(t *testing.T) {
	declared := dedupeSorted(declaredUnionHooks(t))
	called := dedupeSorted(calledUnionHooks(t))
	wired := dedupeSorted(wiredUnionHooks(t))

	if len(declared) == 0 {
		t.Fatal("no Union* hooks found on account.Service: the gate would pass vacuously")
	}
	if strings.Join(declared, ",") != strings.Join(called, ",") {
		t.Fatalf("declared %v but account/players.go calls %v — a hook nobody calls is a silent gap", declared, called)
	}
	if strings.Join(declared, ",") != strings.Join(wired, ",") {
		t.Fatalf("declared %v but main.go wires %v — the hub never hears about the un-wired leg", declared, wired)
	}
}

func dedupeSorted(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
