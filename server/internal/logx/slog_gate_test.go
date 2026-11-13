package logx

// slog_gate_test.go — the structured-log migration is only finished when this
// file's exemption list is empty, and only safe afterwards because the list
// cannot grow again without a failing test.
//
// Why a gate and not a review: 107 call sites across twelve packages drifted
// into bare log.Print over three years, and the next one added will be the
// same one-line convenience. The gate walks every non-test file, finds calls
// to the standard library logger, and fails on any file that is not on the
// pending list. A file leaves the list only in the same commit that converts
// it, so every conversion is guarded from that moment on — removing an
// slog call reintroduces a bare one only if somebody also edits this list,
// which is the part review should look at.
//
// The red reading that started this: with the pending list empty, the gate
// reported 91 Print* and 16 Fatal* call sites across twelve packages.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pending lists files that still call the standard library logger. It starts
// as every offender and shrinks to nothing; see the package comment.
var pending = []string{
	"internal/accesslog/accesslog.go",
	"internal/account/delete.go",
	"internal/account/textures.go",
	"internal/config/env.go",
	"internal/dbx/dbx.go",
	"internal/outbound/scoped.go",
	"internal/plugin/host.go",
	"internal/plugin/runtime_goja.go",
	"internal/store/access_detail.go",
	"internal/theme/theme.go",
	"internal/ygg/service.go",
}

// bareLog reports whether n is a call to the standard library's logger
// (log.Print*, log.Fatal*, log.Panic*). Only a selector on an identifier
// literally named log counts, and only when that file imports "log" — a local
// variable called log is not the logger, and a package that never imports it
// cannot be calling it.
func bareLog(f *ast.File, n ast.Node) bool {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "log" {
		return false
	}
	if !importsStdlibLog(f) {
		return false
	}
	name := sel.Sel.Name
	return strings.HasPrefix(name, "Print") ||
		strings.HasPrefix(name, "Fatal") ||
		strings.HasPrefix(name, "Panic")
}

func importsStdlibLog(f *ast.File) bool {
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		if path == "log" {
			return true
		}
	}
	return false
}

func TestNoBareStandardLibraryLogging(t *testing.T) {
	root := filepath.Join("..", "..")
	onPending := map[string]bool{}
	for _, p := range pending {
		onPending[filepath.Join(root, filepath.FromSlash(p))] = true
	}

	var offenders []string
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		// this package owns the gate and the Fatal helper; nothing else lives here
		if filepath.Base(filepath.Dir(p)) == "logx" {
			return nil
		}
		fset := token.NewFileSet()
		f, perr := parser.ParseFile(fset, p, nil, 0)
		if perr != nil {
			return nil // a file that does not parse fails its own package's build
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		if onPending[p] {
			return nil // still migrating: the conversion commit removes this line
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if bareLog(f, n) {
				pos := fset.Position(n.Pos())
				offenders = append(offenders, filepath.ToSlash(rel)+":"+itoa(pos.Line))
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(offenders) > 0 {
		t.Fatalf("bare standard library logging in %d place(s); use slog (or logx.Fatal when the process must exit):\n  %s",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

// The pending list must name real files: a typo would silently exempt nothing
// and turn the gate red on a file nobody meant to touch, or worse, exempt a
// file that was never migrated.
func TestPendingListNamesRealFiles(t *testing.T) {
	root := filepath.Join("..", "..")
	for _, p := range pending {
		if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(p))); err != nil {
			t.Errorf("pending entry %q does not exist: %v", p, err)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
