package webapi

// The login form accepts "email or nickname" in one field. Rate limiting needs
// the value as typed; the access log must not keep it: `access_logs.who` is
// searchable in the panel and retained for the whole log window, so an
// un-masked identifier there is a permanent copy of every address that failed
// to sign in. This gate scans the package so a new log call cannot reintroduce
// it the way a sibling call site usually gets forgotten.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestLoginLogWritesNoRawIdentifier(t *testing.T) {
	fset := token.NewFileSet()
	pkg, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	seen := 0
	for _, p := range pkg {
		for name, astFile := range p.Files {
			lines := strings.Split(readSource(t, name), "\n")
			ast.Inspect(astFile, func(n ast.Node) bool {
				kv, ok := n.(*ast.KeyValueExpr)
				if !ok {
					return true
				}
				if key, ok := kv.Key.(*ast.Ident); !ok || key.Name != "Who" {
					return true
				}
				line := fset.Position(kv.Value.Pos()).Line
				if line < 1 || line > len(lines) {
					return true
				}
				src := lines[line-1]
				if !strings.Contains(src, "Identifier") {
					return true
				}
				seen++
				if !strings.Contains(src, "redact.Identifier(") {
					t.Errorf("%s:%d: the access-log field Who carries a raw identifier: %s",
						name, line, strings.TrimSpace(src))
				}
				return true
			})
		}
	}
	if seen == 0 {
		t.Fatal("no log call writes a login identifier any more; this gate has become vacuous")
	}
}

func readSource(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
