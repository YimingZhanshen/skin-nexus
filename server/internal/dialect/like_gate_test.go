package dialect

// The wildcard escaping is only a fix while nobody forgets it. A search box that
// concatenates its own "%" around user text puts the wildcards back, so the
// pattern-building sites are pinned by this gate instead of left to review.
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

var likeLayers = []string{
	filepath.Join("internal", "store"),
	filepath.Join("internal", "jobs"),
}

func TestNoHandBuiltLikePatterns(t *testing.T) {
	root := filepath.Join("..", "..")
	var findings []string
	seen := map[string]bool{}
	for _, dir := range likeLayers {
		_ = filepath.Walk(filepath.Join(root, dir), func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			fset := token.NewFileSet()
			f, perr := parser.ParseFile(fset, p, nil, 0)
			if perr != nil {
				return nil
			}
			rel, _ := filepath.Rel(root, p)
			ast.Inspect(f, func(n ast.Node) bool {
				bin, ok := n.(*ast.BinaryExpr)
				if !ok || bin.Op != token.ADD {
					return true
				}
				for _, side := range []ast.Expr{bin.X, bin.Y} {
					lit, ok := side.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					v, uerr := strconv.Unquote(lit.Value)
					if uerr != nil || !strings.Contains(v, "%") {
						continue
					}
					if strings.HasPrefix(v, "%") || strings.HasSuffix(v, "%") {
						where := rel + ":" + strconv.Itoa(fset.Position(lit.Pos()).Line)
						if !seen[where] {
							seen[where] = true
							findings = append(findings, where+" builds a LIKE pattern by hand — bind dialect.LikeContains/LikePrefix instead")
						}
					}
				}
				return true
			})
			return nil
		})
	}
	if len(findings) > 0 {
		t.Fatalf("hand-built LIKE patterns re-open the wildcard hole:\n  %s", strings.Join(findings, "\n  "))
	}
}
