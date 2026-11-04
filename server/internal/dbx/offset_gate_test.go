package dbx

// A3 moved the page arithmetic here, but a shared helper is only a rule while
// nothing bypasses it: two store callers kept writing (Page-1)*PerPage after the
// fix landed, and no test noticed, because SQLite reads the wrapped negative as
// "skip nothing" and returns page one. This gate is the part that can actually
// fail on every engine.
import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestNoHandRolledOffsets(t *testing.T) {
	root := filepath.Join("..", "..")
	skip := map[string]bool{
		// Offset itself lives here; this is the one place the product is allowed.
		filepath.Join("internal", "dbx"): true,
	}
	var findings []string
	for _, dir := range []string{"internal", "cmd"} {
		_ = filepath.Walk(filepath.Join(root, dir), func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			for d := range skip {
				if strings.HasPrefix(rel, d+string(filepath.Separator)) {
					return nil
				}
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, p, nil, 0)
			if perr != nil {
				return nil
			}
			ast.Inspect(f, func(n ast.Node) bool {
				mul, ok := n.(*ast.BinaryExpr)
				if !ok || mul.Op != token.MUL {
					return true
				}
				for _, side := range []ast.Expr{mul.X, mul.Y} {
					if !isMinusOne(side) {
						continue
					}
					where := rel + ":" + strconv.Itoa(fset.Position(mul.Pos()).Line)
					findings = append(findings, where+" computes a row offset by hand — use dbx.Offset, which clamps before it multiplies")
				}
				return true
			})
			return nil
		})
	}
	if len(findings) > 0 {
		t.Fatalf("hand-rolled (x-1)*size offsets can wrap negative into a 500 or a wrong page:\n  %s",
			strings.Join(findings, "\n  "))
	}
}

// isMinusOne reports whether e is the shape (something - 1) — a parenthesised
// subtraction whose subtrahend is the integer literal 1.
func isMinusOne(e ast.Expr) bool {
	paren, ok := e.(*ast.ParenExpr)
	if !ok {
		return false
	}
	sub, ok := paren.X.(*ast.BinaryExpr)
	if !ok || sub.Op != token.SUB {
		return false
	}
	lit, ok := sub.Y.(*ast.BasicLit)
	return ok && lit.Kind == token.INT && lit.Value == "1"
}
