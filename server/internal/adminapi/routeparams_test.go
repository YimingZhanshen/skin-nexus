package adminapi

// Route-parameter gate: every `{param}` a mux pattern declares must be read
// with PathValue somewhere in this package, and every PathValue name must be
// declared by some pattern.
//
// Why this exists: the four admin player routes bind `{pid}` while their
// handlers called idParam, which reads "id". Go's ServeMux has no way to
// notice, PathValue answers "", and rename / owner / textures / delete all
// returned `common.bad_id` for every caller — a whole admin surface shipped
// dead and stayed silent because no test ever dispatched these patterns.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

var (
	routePattern = regexp.MustCompile(`^[A-Z]+ /[^"]*\{`)
	paramInBrace = regexp.MustCompile(`\{([a-zA-Z_][a-zA-Z0-9_]*)\}`)
)

// packageFiles lists the non-test sources of this package (the route table and
// the handlers live side by side, so one pass over both is the whole check).
func packageFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if n := e.Name(); strings.HasSuffix(n, ".go") && !strings.HasSuffix(n, "_test.go") {
			out = append(out, n)
		}
	}
	if len(out) == 0 {
		t.Fatal("no package sources found — the extractor is broken")
	}
	return out
}

// scanParams returns the pattern parameters (param -> "file:line" of the
// route that declares it) and the names read through PathValue.
func scanParams(t *testing.T) (map[string][]string, map[string]bool) {
	t.Helper()
	fset := token.NewFileSet()
	declared := map[string][]string{}
	read := map[string]bool{}

	for _, name := range packageFiles(t) {
		af, err := parser.ParseFile(fset, name, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(af, func(n ast.Node) bool {
			switch x := n.(type) {
			case *ast.BasicLit:
				if x.Kind != token.STRING {
					return true
				}
				v := strings.Trim(x.Value, "\"`")
				if !routePattern.MatchString(v) {
					return true
				}
				line := fset.Position(x.Pos()).Line
				for _, m := range paramInBrace.FindAllStringSubmatch(v, -1) {
					declared[m[1]] = append(declared[m[1]], name+":"+strconv.Itoa(line))
				}
			case *ast.CallExpr:
				sel, ok := x.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "PathValue" || len(x.Args) != 1 {
					return true
				}
				if a, ok := x.Args[0].(*ast.BasicLit); ok && a.Kind == token.STRING {
					read[strings.Trim(a.Value, "\"")] = true
				}
			}
			return true
		})
	}
	return declared, read
}

func TestRouteParamsMatchHandlers(t *testing.T) {
	declared, read := scanParams(t)
	if len(declared) == 0 {
		t.Fatal("no route parameters found — the extractor is broken")
	}

	var unread, orphaned []string
	for p, where := range declared {
		if !read[p] {
			unread = append(unread, p+" (declared at "+strings.Join(where, ", ")+")")
		}
	}
	for p := range read {
		if _, ok := declared[p]; !ok {
			orphaned = append(orphaned, p)
		}
	}
	sort.Strings(unread)
	sort.Strings(orphaned)
	if len(unread) > 0 {
		t.Errorf("route parameters no handler ever reads (PathValue will answer \"\"): %v", unread)
	}
	if len(orphaned) > 0 {
		t.Errorf("PathValue reads a parameter no route declares: %v", orphaned)
	}
}
