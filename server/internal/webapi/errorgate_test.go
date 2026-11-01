package webapi

// errorgate_test.go keeps the error envelope honest without demanding that the
// legacy call sites all migrate at once. testdata/error-budget.txt holds one
// line per counter:
//
//	<scope> uncoded  <n>   error responses that carry no stable `code`
//	<scope> chinese  <n>   hard-coded Chinese prose on the error path — the
//	                       frontend cannot translate a message, so those strings
//	                       become English by the end of this round
//
// A count may only go down. The test also fails when a count *drops* without
// the file being updated, because a stale budget silently permits the next
// regression.
//
// The account service is watched from here because its prose reaches the
// browser through webapi's account error mapping; a second gate would only
// duplicate the machinery.

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

const budgetFile = "testdata/error-budget.txt"

type siteKind int

const (
	kindUncoded     siteKind = iota // no machine-readable code
	kindCoded                       // carries a code argument
	kindFromService                 // code comes from the service's own error value
)

// site is one error response emitted from a governed package.
type site struct {
	pos  string
	kind siteKind
	msg  ast.Expr // the human-readable argument, nil when there is none
	code ast.Expr // the machine-readable argument, nil when there is none
}

// scope names one package in the budget file and says what its emitters look
// like. Adding a package to the gate is a table entry, not a rewrite.
type scope struct {
	name string
	dir  string
	// impl lists the function declarations that *are* the envelope. Their
	// bodies produce a response; they are not call sites.
	impl map[string]bool
	// uncoded maps an emitter name to the argument index holding the prose.
	// -1 means "a hand-rolled body": inspect the payload literal instead.
	uncoded map[string]int
	// coded maps an emitter name to {prose index, code index}.
	coded map[string][2]int
}

var scopes = []scope{
	{
		name:    "webapi",
		dir:     ".",
		impl:    map[string]bool{"writeJSON": true, "writeErr": true, "writeErrCode": true, "writeAccountErr": true},
		uncoded: map[string]int{"writeErr": 2},
		coded: map[string][2]int{
			"writeErrCode":    {2, 3},
			"writeAccountErr": {1, -1},
		},
	},
	{
		name: "account",
		dir:  "../account",
		impl: map[string]bool{"errf": true, "errDetails": true},
		// Every account error is built through errf/errDetails, so a call to
		// them without a code constant is the uncoded case.
		uncoded: map[string]int{},
		coded: map[string][2]int{
			"errf":       {1, 0},
			"errDetails": {1, 0},
		},
	},
}

// classify reports what an error-emitting call is within the given scope.
func (sc scope) classify(call *ast.CallExpr) (site, bool) {
	fn, ok := call.Fun.(*ast.Ident)
	if !ok {
		return site{}, false
	}
	if idx, ok := sc.uncoded[fn.Name]; ok {
		if idx == -1 {
			return site{kind: kindUncoded, msg: payloadLiteral(call)}, true
		}
		return site{kind: kindUncoded, msg: argAt(call, idx)}, true
	}
	if shape, ok := sc.coded[fn.Name]; ok {
		kind := kindCoded
		if shape[1] == -1 {
			kind = kindFromService
		}
		return site{kind: kind, msg: argAt(call, shape[0]), code: argAt(call, shape[1])}, true
	}
	// writeJSON with a hand-rolled error body is the same defect wearing a
	// different shape, so it is counted the same way. Only bodies that carry an
	// "error" key are error responses.
	if fn.Name == "writeJSON" && hasErrorKey(call) {
		if code := valueForKey(call, "code"); code != nil {
			// A body that names its own code is coded — it just bypasses the
			// helper, which is legal when it carries extra keys (plugin, field).
			return site{kind: kindCoded, msg: valueForKey(call, "message"), code: code}, true
		}
		return site{kind: kindUncoded, msg: payloadLiteral(call)}, true
	}
	return site{}, false
}

func argAt(call *ast.CallExpr, i int) ast.Expr {
	if i < 0 || i >= len(call.Args) {
		return nil
	}
	return call.Args[i]
}

// hasErrorKey reports whether a writeJSON payload literal carries "error".
func hasErrorKey(call *ast.CallExpr) bool {
	lit, ok := payloadLiteral(call).(*ast.CompositeLit)
	if !ok {
		return false
	}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if k, ok := kv.Key.(*ast.Ident); ok && k.Name == "Error" {
			return true
		}
		if s, ok := stringLit(kv.Key); ok && s == "error" {
			return true
		}
	}
	return false
}

// payloadLiteral is the first map literal argument of a call — the response
// body written by writeJSON.
func payloadLiteral(call *ast.CallExpr) ast.Expr {
	for _, arg := range call.Args {
		if _, ok := arg.(*ast.CompositeLit); ok {
			return arg
		}
	}
	return nil
}

// valueForKey returns the expression bound to a key in the payload literal, so
// a hand-rolled body's own `code`/`message` is reachable.
func valueForKey(call *ast.CallExpr, key string) ast.Expr {
	lit, ok := payloadLiteral(call).(*ast.CompositeLit)
	if !ok {
		return nil
	}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if s, ok := stringLit(kv.Key); ok && s == key {
			return kv.Value
		}
	}
	return nil
}

func stringLit(n ast.Expr) (string, bool) {
	lit, ok := n.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

func containsHan(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

// hanIn reports whether an argument contains a Chinese string literal — one
// expression deep, so a variable or an err.Error() never trips the gate.
func hanIn(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		if s, err := strconv.Unquote(lit.Value); err == nil && containsHan(s) {
			found = true
		}
		return true
	})
	return found
}

// sitesIn parses every non-test source of a scope and returns its emissions.
func sitesIn(t *testing.T, sc scope) []site {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(sc.dir, "*.go"))
	if err != nil {
		t.Fatalf("glob %s: %v", sc.dir, err)
	}
	if len(files) == 0 {
		t.Fatalf("no sources under %s — the package moved and this gate is now blind", sc.dir)
	}
	var out []site
	fset := token.NewFileSet()
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, decl := range file.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil || sc.impl[fd.Name.Name] {
				continue
			}
			ast.Inspect(fd.Body, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				s, ok := sc.classify(call)
				if !ok {
					return true
				}
				s.pos = fset.Position(call.Pos()).String()
				out = append(out, s)
				return true
			})
		}
	}
	return out
}

// counts is the pair of counters a scope is measured by.
type counts struct{ uncoded, chinese int }

func measure(sites []site) counts {
	var c counts
	for _, s := range sites {
		if s.kind == kindUncoded {
			c.uncoded++
		}
		if hanIn(s.msg) {
			c.chinese++
		}
	}
	return c
}

// readBudget returns scope→counts from the file, and insists on both counters
// so a renamed scope cannot quietly drop out of the gate.
func readBudget(t *testing.T) map[string]counts {
	t.Helper()
	raw, err := os.ReadFile(budgetFile)
	if err != nil {
		t.Fatalf("read %s: %v (write the measured numbers into it)", budgetFile, err)
	}
	out := map[string]counts{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 3 {
			t.Fatalf("%s: expected `<scope> <counter> <n>`, got %q", budgetFile, line)
		}
		n, err := strconv.Atoi(parts[2])
		if err != nil {
			t.Fatalf("%s: %q is not a number", budgetFile, parts[2])
		}
		c := out[parts[0]]
		switch parts[1] {
		case "uncoded":
			c.uncoded = n
		case "chinese":
			c.chinese = n
		default:
			t.Fatalf("%s: unknown counter %q", budgetFile, parts[1])
		}
		out[parts[0]] = c
	}
	for _, sc := range scopes {
		c, ok := out[sc.name]
		if !ok {
			t.Fatalf("%s: no lines for scope %q", budgetFile, sc.name)
		}
		_ = c
	}
	return out
}

func TestErrorBudget(t *testing.T) {
	want := readBudget(t)
	for _, sc := range scopes {
		sites := sitesIn(t, sc)
		got := measure(sites)
		b := want[sc.name]

		if got.uncoded != b.uncoded {
			if got.uncoded > b.uncoded {
				t.Errorf("%s: uncoded error responses grew to %d (budget %d): answer with a code from internal/i18n", sc.name, got.uncoded, b.uncoded)
			} else {
				t.Errorf("%s: budget stale: write `%s uncoded %d` in %s", sc.name, sc.name, got.uncoded, budgetFile)
			}
		}
		if got.chinese != b.chinese {
			if got.chinese > b.chinese {
				t.Errorf("%s: hard-coded Chinese error prose grew to %d (budget %d): the code carries the meaning, the message is English", sc.name, got.chinese, b.chinese)
			} else {
				t.Errorf("%s: budget stale: write `%s chinese %d` in %s", sc.name, sc.name, got.chinese, budgetFile)
			}
		}
		if got.uncoded != b.uncoded || got.chinese != b.chinese {
			for _, s := range sites {
				if s.kind == kindUncoded || hanIn(s.msg) {
					t.Logf("%s", s.pos)
				}
			}
		}
	}
}

// TestCodesComeFromCatalogue is the anti-typo half of the gate: a code written
// as an ad-hoc literal never reaches a language pack, so the user sees raw
// prose with a string only this call site knows about.
func TestCodesComeFromCatalogue(t *testing.T) {
	catalog := codeConsts(t)
	for _, sc := range scopes {
		for _, s := range sitesIn(t, sc) {
			if s.kind != kindCoded {
				continue
			}
			name := ""
			switch code := s.code.(type) {
			case *ast.SelectorExpr:
				name = code.Sel.Name
			case *ast.Ident:
				name = code.Name
			}
			if name == "" {
				t.Errorf("%s: the code argument must be a constant, not %s", s.pos, exprText(s.code))
				continue
			}
			if _, ok := catalog[name]; !ok {
				t.Errorf("%s: %s is not an exported string constant in internal/i18n or internal/account", s.pos, name)
			}
		}
	}
}

func exprText(n ast.Expr) string {
	if n == nil {
		return "<missing>"
	}
	switch n.(type) {
	case *ast.BasicLit:
		return "a string literal"
	case *ast.CallExpr:
		return "a function result"
	}
	return "a computed value"
}

// codeOwners are the packages allowed to declare an error code.
var codeOwners = []string{"../i18n", "../account"}

// codeConsts is every exported string constant in the two code-owning packages,
// as name→value. Parsed from source rather than imported through a list so a
// newly added code cannot be forgotten by the gates that check coverage.
func codeConsts(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	fset := token.NewFileSet()
	for _, dir := range codeOwners {
		files, err := filepath.Glob(filepath.Join(dir, "*.go"))
		if err != nil {
			t.Fatalf("glob %s: %v", dir, err)
		}
		if len(files) == 0 {
			t.Fatalf("no sources under %s — the code owner moved and this gate is now blind", dir)
		}
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			file, err := parser.ParseFile(fset, f, nil, 0)
			if err != nil {
				t.Fatalf("parse %s: %v", f, err)
			}
			for _, decl := range file.Decls {
				gen, ok := decl.(*ast.GenDecl)
				if !ok || gen.Tok != token.CONST {
					continue
				}
				for _, spec := range gen.Specs {
					vs, ok := spec.(*ast.ValueSpec)
					if !ok || len(vs.Values) == 0 {
						continue
					}
					lit, ok := vs.Values[0].(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					val, err := strconv.Unquote(lit.Value)
					if err != nil {
						continue
					}
					for _, name := range vs.Names {
						if name.IsExported() {
							out[name.Name] = val
						}
					}
				}
			}
		}
	}
	if len(out) == 0 {
		t.Fatal("no code constants found — nothing would pass the code check")
	}
	return out
}
