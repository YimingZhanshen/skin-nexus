package i18n

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// codePattern is the naming contract: <area>.<reason>, lowercase, dot-separated.
var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z0-9_]+)+$`)

type codeConst struct {
	name, value string
}

// stringConsts reads every `Name = "value"` from a package's const blocks, so
// the checks run against the source of truth instead of a copy that can drift.
func stringConsts(t *testing.T, pattern string) []codeConst {
	t.Helper()
	files, err := filepath.Glob(pattern)
	if err != nil || len(files) == 0 {
		t.Fatalf("glob %s: %v (%d files)", pattern, err, len(files))
	}
	fset := token.NewFileSet()
	var out []codeConst
	for _, f := range files {
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		ast.Inspect(file, func(n ast.Node) bool {
			vs, ok := n.(*ast.ValueSpec)
			if !ok {
				return true
			}
			for i, name := range vs.Names {
				if i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				if s, err := strconv.Unquote(lit.Value); err == nil && s != "" {
					out = append(out, codeConst{name: name.Name, value: s})
				}
			}
			return true
		})
	}
	return out
}

func TestCodeCatalogue(t *testing.T) {
	consts := stringConsts(t, "codes.go")
	if len(consts) < 40 {
		t.Fatalf("parsed %d code constants, want the whole catalogue", len(consts))
	}
	byValue := map[string]string{}
	for _, c := range consts {
		if !codePattern.MatchString(c.value) {
			t.Errorf("%s = %q must be <area>.<reason> lowercase dot-separated", c.name, c.value)
		}
		if strings.Contains(c.value, "skin-nexus") || strings.Contains(c.value, "/") {
			t.Errorf("%s = %q must not embed module paths or slashes", c.name, c.value)
		}
		if prev, dup := byValue[c.value]; dup {
			// One value meaning two things is worse than no code: the frontend
			// can only translate it one way.
			t.Errorf("%s and %s both map to %q", prev, c.name, c.value)
		}
		byValue[c.value] = c.name
	}
}

// The account service owns the business codes (it raises them and the plugin
// bridge forwards them verbatim). Redefining one here would make the frontend
// look up a key the envelope never carries.
func TestCodesDoNotCollideWithAccount(t *testing.T) {
	biz := stringConsts(t, filepath.Join("..", "account", "service.go"))
	ours := map[string]bool{}
	for _, c := range stringConsts(t, "codes.go") {
		ours[c.value] = true
	}
	if len(biz) < 10 {
		t.Fatalf("parsed %d account codes, want the whole set", len(biz))
	}
	for _, c := range biz {
		if !codePattern.MatchString(c.value) && c.value != "internal" {
			t.Errorf("account code %s = %q violates the naming contract", c.name, c.value)
		}
		if ours[c.value] {
			t.Errorf("code %q is declared in both i18n and account", c.value)
		}
	}
}
