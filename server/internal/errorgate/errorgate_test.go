package errorgate

// These tests cover the engine itself. The packages under the gate declare their
// own scopes (webapi, account, adminapi), but Go attributes coverage per
// package, so the engine sat at 0% while its callers passed — which is exactly
// the position a gate should not be in: nothing pinned what classify counts, how
// the two budgets are tallied, or that prose reached through a variable is not
// the same thing as prose hard-coded at the call site.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"
)

// mixedSource is the fixture the counting tests read. It is only ever parsed,
// so it may name helpers and constants that exist nowhere else.
const mixedSource = `package sample

import "net/http"

func writeErr(w http.ResponseWriter, status int, msg string)              {}
func writeErrCode(w http.ResponseWriter, status int, msg, code string)    {}
func writeJSON(w http.ResponseWriter, status int, body any)               {}
func writeBlob(w http.ResponseWriter, status int, body map[string]string) {}

func uncodedChinese(w http.ResponseWriter) {
	writeErr(w, 400, "材质不存在")
}

func codedClean(w http.ResponseWriter) {
	writeErrCode(w, 400, "texture not found", "texture.not_found")
}

func codedChinese(w http.ResponseWriter) {
	writeErrCode(w, 400, "已存在", MsgDup)
}

func bodyUncoded(w http.ResponseWriter) {
	writeJSON(w, 400, map[string]any{"error": "坏了"})
}

func bodyCoded(w http.ResponseWriter) {
	writeJSON(w, 400, map[string]any{"error": "nope", "code": "a.b", "message": "hello"})
}

func notAnErrorBody(w http.ResponseWriter) {
	writeJSON(w, 200, map[string]any{"ok": true})
}

func computedCode(w http.ResponseWriter) {
	writeErrCode(w, 400, "gone", pick())
}

func blobSite(w http.ResponseWriter) {
	writeBlob(w, 400, map[string]string{"error": "路径不通"})
}
`

// codedSource is the fixture for the code-owner check: every code argument
// names a constant the owner directory declares.
const codedSource = `package sample

import "net/http"

func writeErrCode(w http.ResponseWriter, status int, msg, code string) {}

func ok1(w http.ResponseWriter) { writeErrCode(w, 400, "gone", MsgMissing) }
func ok2(w http.ResponseWriter) { writeErrCode(w, 400, "dupe", MsgDup) }
`

const ownerSource = `package owners

const (
	MsgMissing = "texture.not_found"
	MsgDup     = "texture.duplicate"
	handWire   = "unexported, must stay invisible to the check"
)

const NotAString = 7
const Computed = MsgMissing + "!"
`

func writeFixture(t *testing.T, name, content string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return dir
}

func mixedScope(dir string) Scope {
	return Scope{
		Name: "fixture",
		Dir:  dir,
		Impl: []string{"writeErr", "writeErrCode", "writeJSON", "writeBlob"},
		Uncoded: []Emitter{
			{Name: "writeErr", Prose: 2, Code: -1},
			{Name: "writeBlob", Prose: -1, Code: -1}, // whole-body prose
		},
		Coded: []Emitter{{Name: "writeErrCode", Prose: 2, Code: 3}},
		Body:  "writeJSON",
	}
}

func TestHanMatchesIdeographsOnly(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"texture not found", false},
		{"材质不存在", true},
		{"，。：、", false}, // fullwidth punctuation is not an ideograph
		{"™·→", false},  // symbols are not an ideograph
		{"ＡＢＣ", false},  // fullwidth latin
		{"ok 好的", true}, // mixed copy still counts
		{"", false},
	}
	for _, c := range cases {
		if got := Han(c.in); got != c.want {
			t.Errorf("Han(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// callArgs parses the fixture and hands back each call's first argument, keyed
// by the callee name, so HasHan is tested on real expressions.
func callArgs(t *testing.T, src string) map[string]ast.Expr {
	t.Helper()
	dir := writeFixture(t, "one.go", src)
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filepath.Join(dir, "one.go"), nil, 0)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	out := map[string]ast.Expr{}
	ast.Inspect(file, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		if name := CallName(call); name != "" {
			out[name] = call.Args[0]
		}
		return true
	})
	return out
}

func TestHasHanReadsLiteralsInsideTheExpression(t *testing.T) {
	args := callArgs(t, `package p

var variable = "中文"

func f() {
	concat("拼接" + "tail")
	nested(inner("中文"))
	passes(variable)
	fromCall(result())
	plain("clean")
	integer(7)
}
`)
	cases := []struct {
		callee string
		want   bool
		why    string
	}{
		{"concat", true, "a literal inside a concatenation is still hard-coded prose"},
		{"nested", true, "a literal anywhere in the argument tree was written at this site"},
		{"passes", false, "a variable reference is not a literal — resolving it is not the gate's job"},
		{"fromCall", false, "a function result is not a literal"},
		{"plain", false, "English prose"},
		{"integer", false, "not a string at all"},
	}
	for _, c := range cases {
		expr, ok := args[c.callee]
		if !ok {
			t.Fatalf("fixture lost the %s call", c.callee)
		}
		if got := HasHan(expr); got != c.want {
			t.Errorf("HasHan(%s arg) = %v, want %v — %s", c.callee, got, c.want, c.why)
		}
	}
	if HasHan(nil) {
		t.Error("HasHan(nil) must be false: an emitter may take no prose argument")
	}
}

func TestSitesAndMeasureCountBothDebts(t *testing.T) {
	sites := Sites(t, mixedScope(writeFixture(t, "mixed.go", mixedSource)))

	// Hand-counted from the fixture: seven declared calls that emit a response.
	// notAnErrorBody is excluded — a writeJSON body without an "error" key is a
	// success response, and counting it would flag every ordinary write.
	if len(sites) != 7 {
		t.Fatalf("sites = %d, want 7: %+v", len(sites), positions(sites))
	}
	counts, bad := Measure(sites)
	// uncoded: writeErr, the {"error"} body, writeBlob.
	if counts.Uncoded != 3 {
		t.Errorf("uncoded = %d, want 3: %+v", counts.Uncoded, positions(bad))
	}
	// chinese: 材质不存在, 已存在, 坏了, 路径不通.
	if counts.Chinese != 4 {
		t.Errorf("chinese = %d, want 4: %+v", counts.Chinese, positions(bad))
	}
	// 材质/坏了/路径不通 are uncoded *and* Chinese, 已存在 is Chinese with a code:
	// four distinct rows, not seven.
	if len(bad) != 4 {
		t.Errorf("reported sites = %d, want 4: %+v", len(bad), positions(bad))
	}
	for _, s := range bad {
		if s.Coded && !s.Han {
			t.Errorf("%s reported as an offender while it is neither uncoded nor Chinese", s.Pos)
		}
	}
	byEmitter := map[string]int{}
	for _, s := range sites {
		byEmitter[s.Emitter]++
	}
	for name, want := range map[string]int{"writeErr": 1, "writeErrCode": 3, "writeJSON": 2, "writeBlob": 1} {
		if byEmitter[name] != want {
			t.Errorf("emitter %s matched %d sites, want %d", name, byEmitter[name], want)
		}
	}
	// The declared Impl helpers are the envelope's own bodies: their names match
	// the emitter lists, so a walk that did not skip them would count the
	// definition site as a call site. The fixture's four helpers have empty
	// bodies, so the seven sites above are exactly the calls.
}

func TestProseMinusOneInspectsTheWholeBody(t *testing.T) {
	sites := Sites(t, mixedScope(writeFixture(t, "mixed.go", mixedSource)))
	var blob *Site
	for i := range sites {
		if sites[i].Emitter == "writeBlob" {
			blob = &sites[i]
		}
	}
	if blob == nil {
		t.Fatal("writeBlob was not matched, so Prose:-1 is untested")
	}
	lit, ok := blob.Prose.(*ast.CompositeLit)
	if !ok {
		t.Fatalf("whole-body prose = %T, want the map literal itself", blob.Prose)
	}
	if len(lit.Elts) != 1 {
		t.Errorf("body literal has %d entries, want the one the fixture wrote", len(lit.Elts))
	}
}

func TestSiteWhyNamesTheReason(t *testing.T) {
	cases := []struct {
		s    Site
		want string
	}{
		{Site{Coded: false, Han: true}, "no code, Chinese prose"},
		{Site{Coded: false, Han: false}, "no code"},
		{Site{Coded: true, Han: true}, "Chinese prose"},
	}
	for i, c := range cases {
		if got := siteWhy(c.s); got != c.want {
			t.Errorf("case %d: siteWhy = %q, want %q", i, got, c.want)
		}
	}
}

func TestCodeArgumentIsClassifiedByShape(t *testing.T) {
	sites := Sites(t, mixedScope(writeFixture(t, "mixed.go", mixedSource)))
	var constant, literal, computed int
	for _, s := range sites {
		if !s.Coded {
			continue
		}
		switch s.CodeName {
		case "MsgDup":
			constant++
		case "":
			switch shapeOf(s.Code) {
			case "a string literal":
				literal++
			case "a function result":
				computed++
			}
		default:
			t.Errorf("unexpected code name %q at %s", s.CodeName, s.Pos)
		}
	}
	// Three coded sites: MsgDup (a constant), two literals
	// ("texture.not_found", "a.b" — the body path counts too), and pick().
	if constant != 1 || literal != 2 || computed != 1 {
		t.Errorf("coded shapes = constant %d literal %d computed %d, want 1/2/1",
			constant, literal, computed)
	}
}

func TestCheckCodesAcceptsSitesNamingOwnerConstants(t *testing.T) {
	codeDir := writeFixture(t, "coded.go", codedSource)
	ownerDir := writeFixture(t, "owners.go", ownerSource)

	known := StringConsts(t, []string{ownerDir})
	if len(known) != 2 {
		t.Fatalf("StringConsts = %+v, want exactly the two exported string constants", known)
	}
	for _, gone := range []string{"handWire", "NotAString", "Computed"} {
		if _, ok := known[gone]; ok {
			t.Errorf("%s leaked into the code set", gone)
		}
	}
	if known["MsgMissing"] != "texture.not_found" {
		t.Errorf("MsgMissing = %q, want its literal value", known["MsgMissing"])
	}

	sc := Scope{
		Name:       "coded",
		Dir:        codeDir,
		Coded:      []Emitter{{Name: "writeErrCode", Prose: 2, Code: 3}},
		CodeOwners: []string{ownerDir},
	}
	// Would call t.Errorf for any site whose code is not an owner constant.
	CheckCodes(t, sc)
	for _, s := range Sites(t, sc) {
		value, ok := known[s.CodeName]
		if !ok {
			t.Errorf("%s: code %q is unknown to the owner set", s.Pos, s.CodeName)
			continue
		}
		if !CodePattern.MatchString(value) {
			t.Errorf("%s: value %q violates the <area>.<reason> shape", s.Pos, value)
		}
	}
}

func TestCallNameReadsTheInvokedFunction(t *testing.T) {
	args := callArgs(t, `package p

func f() {
	err.Error()
	pkg.Fn()
	local()
	a.Chained()
	_ = notACall
}
`)
	for name := range args {
		if name == "" {
			t.Error("CallName returned an empty name for a call the fixture can name")
		}
	}
	// CallName is exported and used directly by the leak checks.
	if got := CallName(nil); got != "" {
		t.Errorf("CallName(nil) = %q, want empty", got)
	}
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "x", `package p
func f() { q() }`, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	var got string
	ast.Inspect(file, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			got = CallName(call)
		}
		return true
	})
	if got != "q" {
		t.Errorf("CallName(bare call) = %q, want q", got)
	}
}

func TestCodePatternShape(t *testing.T) {
	cases := map[string]bool{
		"texture.not_found":   true,
		"a.b.c":               true,
		"texture.not_found.x": true,
		"internal":            true, // the account service's 500 fallback
		"texture":             false,
		".texture":            false,
		"Texture.not_found":   false,
		"texture.":            false,
		"texture.not found":   false,
		"1nvalid.code":        false,
		"ok.中文":               false,
	}
	for in, want := range cases {
		if got := CodePattern.MatchString(in); got != want {
			t.Errorf("CodePattern.MatchString(%q) = %v, want %v", in, got, want)
		}
	}
}

func positions(sites []Site) []string {
	out := make([]string, 0, len(sites))
	for _, s := range sites {
		out = append(out, s.Emitter+":"+filepath.Base(s.Pos))
	}
	return out
}
