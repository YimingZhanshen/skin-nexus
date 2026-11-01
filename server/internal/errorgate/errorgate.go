// Package errorgate is the AST gate behind the HTTP error envelope.
//
// The envelope is `{error, code, message}`: `code` is what a language pack can
// translate, `message` is the fallback a client shows when it does not know the
// code, and prose hard-coded in Chinese is a message no English UI can render.
// Migrating a package with a hundred call sites in one commit is not realistic,
// so this gate counts two things per package and puts them in a budget file
// that is only allowed to shrink:
//
//	uncoded  error responses emitted without a stable `code`
//	chinese  error responses whose prose is a Chinese string literal
//
// A count that shrinks without the budget file being updated also fails: a
// budget that overstates the debt hides the next regression.
//
// The checks run at the syntax level rather than by calling the handlers,
// because most error branches sit behind a database. Callers declare a Scope —
// which helper functions in that package emit a response, and which argument of
// each holds the prose and the code — and the engine reads the sources.
package errorgate

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

// Emitter names one function that writes an error response.
type Emitter struct {
	// Name is the function as it appears at the call site.
	Name string
	// Prose is the argument index holding the human-readable message, or -1 to
	// inspect the whole response body (a hand-rolled map literal).
	Prose int
	// Code is the argument index holding the machine-readable code, or -1 when
	// the emitter does not take one.
	Code int
}

// Scope is one package under the gate.
type Scope struct {
	// Name is the label used in the budget file.
	Name string
	// Dir is the package directory, relative to the test's working directory
	// (the package that runs the gate).
	Dir string
	// Impl names the functions that *are* the envelope. Their bodies build a
	// response; they are not call sites and are never counted.
	Impl []string
	// Uncoded lists emitters that produce a response with no code.
	Uncoded []Emitter
	// Coded lists emitters that require a code argument.
	Coded []Emitter
	// Body is a generic "write this JSON" helper whose map literal counts as an
	// error response when it carries an "error" key — a call site that skips the
	// error helpers entirely is the same defect in a different shape.
	Body string
	// CodeOwners are directories whose exported string constants may be used as
	// a code.
	CodeOwners []string
}

// Site is one error response emitted from a governed package.
type Site struct {
	Pos   string
	Scope string
	// Coded is false when the response carries no machine-readable code.
	Coded bool
	// Prose is the message argument, nil when there is none.
	Prose ast.Expr
	// Code is the code argument, nil when absent or supplied by a service.
	Code ast.Expr
	// CodeName is the constant named by Code ("" when it is not a constant).
	CodeName string
	// Han is filled by Measure: the prose argument holds a Chinese literal.
	Han bool
	// Emitter is the function that wrote the response. A caller with a policy of
	// its own (declared-domain prose, for instance) filters on it.
	Emitter string
}

// CodePattern is the namespace rule an error code must satisfy:
// `<area>.<reason>`, lowercase, dot-separated — plus the single unprefixed
// `internal` the account service uses for its 500 fallback.
var CodePattern = regexp.MustCompile(`^([a-z][a-z0-9_]*(\.[a-z0-9_]+)+|internal)$`)

// Han returns true when s contains a CJK ideograph.
func Han(s string) bool {
	for _, r := range s {
		if r >= 0x4E00 && r <= 0x9FFF {
			return true
		}
	}
	return false
}

// hanIn reports whether an expression contains a Chinese string literal, looked
// at one expression deep so a variable or a function result never trips it.
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
		if s, err := strconv.Unquote(lit.Value); err == nil && Han(s) {
			found = true
		}
		return true
	})
	return found
}

// HasHan reports whether an argument contains a Chinese string literal — one
// expression deep, so a variable or a function result never trips the gate.
func HasHan(expr ast.Expr) bool { return hanIn(expr) }

// CallName is the method or function an expression invokes (`err.Error()` →
// "Error"), used by the leak check.
func CallName(expr ast.Expr) string {
	call, ok := expr.(*ast.CallExpr)
	if !ok {
		return ""
	}
	switch fn := call.Fun.(type) {
	case *ast.Ident:
		return fn.Name
	case *ast.SelectorExpr:
		return fn.Sel.Name
	}
	return ""
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

func argAt(call *ast.CallExpr, i int) ast.Expr {
	if i < 0 || i >= len(call.Args) {
		return nil
	}
	return call.Args[i]
}

// payloadLiteral is the first map literal argument of a call — the body written
// by a generic JSON helper.
func payloadLiteral(call *ast.CallExpr) ast.Expr {
	for _, arg := range call.Args {
		if _, ok := arg.(*ast.CompositeLit); ok {
			return arg
		}
	}
	return nil
}

func bodyKey(call *ast.CallExpr, key string) (ast.Expr, bool) {
	lit, ok := payloadLiteral(call).(*ast.CompositeLit)
	if !ok {
		return nil, false
	}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if s, ok := stringLit(kv.Key); ok && s == key {
			return kv.Value, true
		}
		if id, ok := kv.Key.(*ast.Ident); ok && strings.EqualFold(id.Name, key) {
			return kv.Value, true
		}
	}
	return nil, false
}

func hasErrorKey(call *ast.CallExpr) bool {
	_, ok := bodyKey(call, "error")
	return ok
}

func (sc Scope) classify(call *ast.CallExpr) (Site, bool) {
	fn, ok := call.Fun.(*ast.Ident)
	if !ok {
		return Site{}, false
	}
	name := fn.Name
	for _, e := range sc.Uncoded {
		if e.Name != name {
			continue
		}
		prose := argAt(call, e.Prose)
		if e.Prose == -1 {
			prose = payloadLiteral(call)
		}
		return Site{Scope: sc.Name, Coded: false, Prose: prose, Emitter: name}, true
	}
	for _, e := range sc.Coded {
		if e.Name != name {
			continue
		}
		prose := argAt(call, e.Prose)
		if e.Prose == -1 {
			prose = payloadLiteral(call)
		}
		code := argAt(call, e.Code)
		return Site{Scope: sc.Name, Coded: true, Prose: prose, Code: code, CodeName: constName(code), Emitter: name}, true
	}
	if sc.Body != "" && name == sc.Body && hasErrorKey(call) {
		code, coded := bodyKey(call, "code")
		if coded {
			return Site{Scope: sc.Name, Coded: true, Prose: proseOf(call), Code: code, CodeName: constName(code), Emitter: name}, true
		}
		return Site{Scope: sc.Name, Coded: false, Prose: payloadLiteral(call), Emitter: name}, true
	}
	return Site{}, false
}

func proseOf(call *ast.CallExpr) ast.Expr {
	v, _ := bodyKey(call, "message")
	return v
}

// constName is the identifier a code argument names: `i18n.MsgX` → MsgX, a bare
// `CodeX` → CodeX, anything else → "" (not a constant).
func constName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		return e.Sel.Name
	case *ast.Ident:
		return e.Name
	}
	return ""
}

// Sites parses every non-test source of a scope and returns its emissions.
func Sites(t *testing.T, sc Scope) []Site {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(sc.Dir, "*.go"))
	if err != nil {
		t.Fatalf("glob %s: %v", sc.Dir, err)
	}
	if len(files) == 0 {
		t.Fatalf("no sources under %s — the package moved and this gate is now blind", sc.Dir)
	}
	skip := map[string]bool{}
	for _, name := range sc.Impl {
		skip[name] = true
	}
	var out []Site
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
			if !ok || fd.Body == nil || skip[fd.Name.Name] {
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
				s.Pos = fset.Position(call.Pos()).String()
				out = append(out, s)
				return true
			})
		}
	}
	return out
}

// Counts is the pair of numbers a scope is budgeted by.
type Counts struct {
	Uncoded int
	Chinese int
}

// Measure tallies a scope's sites and returns the offending ones, annotated, so
// a failure can name them.
func Measure(sites []Site) (Counts, []Site) {
	var c Counts
	var bad []Site
	for i := range sites {
		s := sites[i]
		s.Han = hanIn(s.Prose)
		sites[i] = s
		if !s.Coded {
			c.Uncoded++
		}
		if s.Han {
			c.Chinese++
		}
		if !s.Coded || s.Han {
			bad = append(bad, s)
		}
	}
	return c, bad
}

// AssertClean is the end state of the envelope migration: every error response
// in these scopes carries a stable code, and no prose is hard-coded in Chinese.
//
// It used to be a budget file that only allowed the two counters to shrink,
// because a package with 184 legacy call sites cannot be fixed in one commit.
// Both counters reached zero, so the file is gone and the rule is absolute —
// a new uncoded response or Chinese literal fails the build outright.
func AssertClean(t *testing.T, scopes ...Scope) {
	t.Helper()
	for _, sc := range scopes {
		_, bad := Measure(Sites(t, sc))
		for _, s := range bad {
			t.Errorf("%s: %s — %s", s.Pos, sc.Name, siteWhy(s))
		}
	}
}

func siteWhy(s Site) string {
	switch {
	case !s.Coded && s.Han:
		return "no code, Chinese prose"
	case !s.Coded:
		return "no code"
	default:
		return "Chinese prose"
	}
}

// CheckCodes asserts that every coded site names an exported string constant of
// one of the owner packages. A code written as an ad-hoc literal never reaches a
// language pack, so the user gets prose and the client gets a string only that
// one call site knows.
func CheckCodes(t *testing.T, scopes ...Scope) {
	t.Helper()
	owners := []string{}
	for _, sc := range scopes {
		owners = append(owners, sc.CodeOwners...)
	}
	known := StringConsts(t, dedupe(owners))
	for _, sc := range scopes {
		for _, s := range Sites(t, sc) {
			if s.Code == nil {
				// Coded through a service's own error value: the constant check
				// belongs to the package that raises it, not this one.
				continue
			}
			if s.CodeName == "" {
				t.Errorf("%s: the code argument must be a constant, not %s", s.Pos, shapeOf(s.Code))
				continue
			}
			if _, ok := known[s.CodeName]; !ok {
				t.Errorf("%s: %s is not an exported string constant in the code-owning packages", s.Pos, s.CodeName)
			}
		}
	}
}

func shapeOf(n ast.Expr) string {
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

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// StringConsts returns every exported string constant in dirs, as name→value.
// Reading them from source instead of a hand-written list means a new code
// cannot be forgotten by the checks that need to know the set.
func StringConsts(t *testing.T, dirs []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	fset := token.NewFileSet()
	for _, dir := range dirs {
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
