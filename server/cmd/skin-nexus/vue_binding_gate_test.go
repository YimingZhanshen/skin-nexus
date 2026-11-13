package main

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// The template reaches into the SFC's setup scope, and the compiler trusts
// that it does: a PascalCase tag with no binding becomes a runtime
// resolveComponent lookup that warns and renders NOTHING — the element simply
// never appears, with no thrown error (W4-3 shipped the dashboard split this
// way: TexturesCard lost its import and the card vanished). An unbound
// identifier compiles to _ctx.x and throws only when the row that uses it
// renders. Both classes are invisible to `vite build` and to any API test,
// so this gate reads the SFC sources directly and asserts what the template
// references is actually in scope.

var (
	vueScriptRE     = regexp.MustCompile(`(?s)<script[^>]*>(.*?)</script>`)
	vueTemplateRE   = regexp.MustCompile(`(?s)<template[^>]*>(.*)</template>`)
	vueCommentRE    = regexp.MustCompile(`(?s)<!--.*?-->`)
	vueTagRE        = regexp.MustCompile(`<([A-Z][A-Za-z0-9]*)`)
	vueAttrExprRE   = regexp.MustCompile(`([@:#][\w.-]+|v-[\w.:-]+)\s*=\s*"([^"]*)"`)
	vueInterpRE     = regexp.MustCompile(`(?s)\{\{(.*?)\}\}`)
	vueCallRE       = regexp.MustCompile(`(^|[^.\w$])([A-Za-z_][\w$]*)[ \t]*\(`)
	vueIdentRE      = regexp.MustCompile(`[A-Za-z_$][\w$]*`)
	vueForRE        = regexp.MustCompile(`v-for\s*=\s*"([^"]*)"`)
	vueInOfRE       = regexp.MustCompile(`\s+(in|of)\s+`)
	vueSlotPropsRE  = regexp.MustCompile(`#\w+\s*=\s*"\{([^}]*)\}"`)
	vueArrowParen   = regexp.MustCompile(`\(\s*([\w\s,]+?)\s*\)\s*=>`)
	vueArrowSingle  = regexp.MustCompile(`([A-Za-z_$][\w$]*)\s*=>`)
	vueFuncParams   = regexp.MustCompile(`function\s*\(([^)]*)\)`)
	vueObjKeyAfter  = regexp.MustCompile(`^[ \t]*:`)
	vueObjKeyBefore = regexp.MustCompile(`[{,][ \t\n]*$`)

	// Top-level only (column 0): the template reaches setup-scope bindings,
	// and a const inside a block or function is not one of them.
	vueImportFrom  = regexp.MustCompile(`(?m)^import[ \t]+(\w+)[ \t]+from`)
	vueImportStar  = regexp.MustCompile(`(?m)^import[ \t]+\*[ \t]+as[ \t]+(\w+)`)
	vueImportNamed = regexp.MustCompile(`(?m)^import[ \t]*\{([^}]*)\}`)
	vueDecl        = regexp.MustCompile(`(?m)^(export[ \t]+)?(const|let|var|function|async[ \t]+function|class)[ \t]+(\w+)`)
	vueDestruct    = regexp.MustCompile(`(?m)^(const|let|var)[ \t]*\{([^}]*)\}[ \t]*=`)
	vueWordRE      = regexp.MustCompile(`^\w+$`)

	vueStrSQ = regexp.MustCompile(`'([^'\\]|\\.)*'`)
	vueStrDQ = regexp.MustCompile(`"([^"\\]|\\.)*"`)
	vueStrBT = regexp.MustCompile("(`([^`\\\\]|\\\\.)*`)")

	// Script-scope checks (the third test): `x.value` users must be declared
	// somewhere in the same script. These are the lenient, any-indent forms —
	// a false negative (a name declared out of use-site scope) costs nothing,
	// a false positive would gate on style.
	vueValueUserRE   = regexp.MustCompile(`(^|[^.\w$])([A-Za-z_][\w$]*)\.value\b`)
	vueDeclAnyRE     = regexp.MustCompile(`(?m)^[ \t]*(export[ \t]+)?(const|let|var|function|async[ \t]+function|class)[ \t]+(\w+)`)
	vueArrDestructRE = regexp.MustCompile(`(const|let|var)[ \t]*\[([^\]]*)\]`)
	vueObjDestructRE = regexp.MustCompile(`(const|let|var)[ \t]*\{([^}]*)\}[ \t]*=`)
	vueForVarRE      = regexp.MustCompile(`for[ \t]*\([ \t]*(?:const|let|var)[ \t]+(\w+)`)
	vueCatchRE       = regexp.MustCompile(`\bcatch[ \t]*\((\w+)`)
	vueFnParamsRE    = regexp.MustCompile(`function[ \t]*\w*[ \t]*\(([^)]*)\)`)
	vueArrowParamsRE = regexp.MustCompile(`\(([^()]*)\)[ \t]*=>`)
)

// vueBuiltinComponents are globally available to every template; everything
// else must be a setup binding.
var vueBuiltinComponents = map[string]bool{
	"Transition": true, "TransitionGroup": true, "KeepAlive": true,
	"Suspense": true, "Teleport": true, "Component": true,
}

// vueTemplateGlobals are the JS globals a template expression may call.
var vueTemplateGlobals = map[string]bool{
	"Math": true, "Number": true, "String": true, "Boolean": true, "JSON": true,
	"Date": true, "Object": true, "Array": true, "parseInt": true, "parseFloat": true,
	"isNaN": true, "isFinite": true, "encodeURIComponent": true, "decodeURIComponent": true,
	"encodeURI": true, "decodeURI": true, "URL": true, "RegExp": true, "Promise": true,
	"BigInt": true, "console": true, "window": true, "document": true, "globalThis": true,
	"setTimeout": true, "clearTimeout": true, "setInterval": true, "clearInterval": true,
	"structuredClone": true, "undefined": true, "null": true, "true": true, "false": true,
	"Infinity": true, "NaN": true,
}

var jsKeywords = map[string]bool{
	"in": true, "of": true, "typeof": true, "instanceof": true, "new": true,
	"void": true, "delete": true, "await": true, "yield": true,
}

type vueExpr struct{ name, code string }

// vueSFCFiles walks web/src for single-file components; the floor catches a
// walker that silently returns nothing (which would make every assertion
// below pass vacuously).
func vueSFCFiles(t *testing.T) []string {
	t.Helper()
	var files []string
	err := filepath.Walk(filepath.Join(repoRoot(t), "web", "src"), func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(p, ".vue") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk web/src: %v", err)
	}
	sort.Strings(files)
	if len(files) < 40 {
		t.Fatalf("found only %d .vue files — the walker is broken", len(files))
	}
	return files
}

func loadVueSFC(t *testing.T, path string) (rel, script, tmpl string) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	rel, _ = filepath.Rel(repoRoot(t), path)
	src := string(b)
	if m := vueScriptRE.FindStringSubmatch(src); m != nil {
		script = m[1]
	}
	if m := vueTemplateRE.FindStringSubmatch(src); m != nil {
		tmpl = vueCommentRE.ReplaceAllString(m[1], "")
	}
	if script == "" {
		t.Fatalf("%s: no <script> block — extractor broken", rel)
	}
	return rel, script, tmpl
}

// vueScriptBindings collects every top-level name the template may reach:
// imports (including the asyncPage(() => import(...)) lazy-page pattern),
// top-level declarations, destructured const bindings, and defineProps keys.
func vueScriptBindings(script string) map[string]bool {
	b := map[string]bool{}
	for _, m := range vueImportFrom.FindAllStringSubmatch(script, -1) {
		b[m[1]] = true
	}
	for _, m := range vueImportStar.FindAllStringSubmatch(script, -1) {
		b[m[1]] = true
	}
	for _, m := range vueImportNamed.FindAllStringSubmatch(script, -1) {
		for _, part := range strings.Split(m[1], ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if i := strings.LastIndex(part, " as "); i >= 0 {
				part = strings.TrimSpace(part[i+4:])
			}
			if part != "" {
				b[part] = true
			}
		}
	}
	for _, m := range vueDecl.FindAllStringSubmatch(script, -1) {
		b[m[3]] = true
	}
	for _, m := range vueDestruct.FindAllStringSubmatch(script, -1) {
		for _, part := range strings.Split(m[2], ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if i := strings.Index(part, ":"); i >= 0 {
				part = strings.TrimSpace(part[i+1:])
			}
			if vueWordRE.MatchString(part) {
				b[part] = true
			}
		}
	}
	vueDefinePropsKeys(script, b)
	return b
}

// vueDefinePropsKeys walks the defineProps object with brace counting: the
// body nests `({})` defaults, so a regex to the first `})` would drop every
// prop declared after the first. Only depth-1 identifiers are prop names;
// `type:`/`default:` live deeper.
func vueDefinePropsKeys(script string, b map[string]bool) {
	const marker = "defineProps("
	dp := strings.Index(script, marker)
	if dp < 0 {
		return
	}
	open := strings.Index(script[dp+len(marker):], "{")
	if open < 0 {
		return
	}
	open += dp + len(marker)
	depth := 0
	for i := open; i < len(script); i++ {
		switch script[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return
			}
		default:
			if depth != 1 || !isJSIdentStart(script[i]) {
				continue
			}
			if i > 0 && isJSIdentPart(script[i-1]) {
				continue // middle of an identifier
			}
			j := i
			for j < len(script) && isJSIdentPart(script[j]) {
				j++
			}
			rest := script[j:]
			if len(rest) > 0 && (rest[0] == ' ' || rest[0] == '\t') {
				k := 0
				for k < len(rest) && (rest[k] == ' ' || rest[k] == '\t') {
					k++
				}
				if k < len(rest) && rest[k] == ':' {
					b[script[i:j]] = true
				}
			} else if len(rest) > 0 && rest[0] == ':' {
				b[script[i:j]] = true
			}
			i = j - 1
		}
	}
}

func isJSIdentStart(c byte) bool {
	return c == '_' || c == '$' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isJSIdentPart(c byte) bool {
	return isJSIdentStart(c) || (c >= '0' && c <= '9')
}

func vueStripStrings(s string) string {
	s = vueStrSQ.ReplaceAllString(s, "''")
	s = vueStrDQ.ReplaceAllString(s, `""`)
	s = vueStrBT.ReplaceAllString(s, "``")
	return s
}

func vueDecodeEntities(s string) string {
	r := strings.NewReplacer("&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&apos;", "'")
	return r.Replace(s)
}

// vueTemplateExprs extracts the expressions the template actually evaluates:
// bound attribute values and interpolations. Plain attributes (style, title,
// placeholder) are literals, and v-for's `x in (list)` reads as a call, so
// both stay out — v-for contributes scope names instead.
func vueTemplateExprs(tmpl string) []vueExpr {
	var out []vueExpr
	for _, m := range vueAttrExprRE.FindAllStringSubmatch(tmpl, -1) {
		if m[1] == "v-for" {
			continue
		}
		out = append(out, vueExpr{name: m[1], code: vueDecodeEntities(m[2])})
	}
	for _, m := range vueInterpRE.FindAllStringSubmatch(tmpl, -1) {
		out = append(out, vueExpr{name: "{{}}", code: m[1]})
	}
	return out
}

// vueScopeVars are the names an expression may legitimately reference without
// a binding: v-for/slot-prop destructuring and inline function parameters.
func vueScopeVars(exprs []vueExpr, tmpl string) map[string]bool {
	s := map[string]bool{}
	addList := func(list string) {
		for _, part := range strings.Split(list, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if i := strings.Index(part, ":"); i >= 0 {
				part = strings.TrimSpace(part[i+1:])
			}
			if vueWordRE.MatchString(part) {
				s[part] = true
			}
		}
	}
	for _, m := range vueForRE.FindAllStringSubmatch(tmpl, -1) {
		left := m[1]
		if idx := vueInOfRE.FindStringIndex(left); idx != nil {
			left = left[:idx[0]]
		}
		left = strings.TrimSpace(left)
		switch {
		case strings.HasPrefix(left, "(") && strings.HasSuffix(left, ")"):
			addList(strings.TrimSuffix(strings.TrimPrefix(left, "("), ")"))
		case strings.HasPrefix(left, "{") && strings.HasSuffix(left, "}"):
			addList(strings.TrimSuffix(strings.TrimPrefix(left, "{"), "}"))
		case left != "":
			s[left] = true
		}
	}
	for _, m := range vueSlotPropsRE.FindAllStringSubmatch(tmpl, -1) {
		addList(m[1])
	}
	for _, e := range exprs {
		for _, m := range vueArrowParen.FindAllStringSubmatch(e.code, -1) {
			addList(m[1])
		}
		for _, m := range vueFuncParams.FindAllStringSubmatch(e.code, -1) {
			addList(m[1])
		}
		for _, m := range vueArrowSingle.FindAllStringSubmatch(e.code, -1) {
			s[m[1]] = true
		}
	}
	return s
}

// vueBareRefs lists identifier tokens that stand alone as references (not
// after a dot, not $-prefixed, not an object-literal key). Strings are
// blanked first so prose like `foo (bar)` inside a label never counts.
func vueBareRefs(code string) []string {
	s := vueStripStrings(code)
	var out []string
	for _, loc := range vueIdentRE.FindAllStringIndex(s, -1) {
		id := s[loc[0]:loc[1]]
		before, after := s[:loc[0]], s[loc[1]:]
		if strings.HasSuffix(before, ".") || strings.HasPrefix(id, "$") || jsKeywords[id] {
			continue
		}
		if vueObjKeyAfter.MatchString(after) && vueObjKeyBefore.MatchString(before) {
			continue
		}
		out = append(out, id)
	}
	return out
}

func TestVueTemplateComponentsAreInScope(t *testing.T) {
	for _, f := range vueSFCFiles(t) {
		rel, script, tmpl := loadVueSFC(t, f)
		if tmpl == "" {
			continue
		}
		bound := vueScriptBindings(script)
		seen := map[string]bool{}
		for _, m := range vueTagRE.FindAllStringSubmatch(tmpl, -1) {
			tag := m[1]
			if seen[tag] || bound[tag] || vueBuiltinComponents[tag] {
				continue
			}
			seen[tag] = true
			t.Errorf("%s: template tag <%s> has no script binding — it compiles to a runtime resolveComponent lookup and renders nothing", rel, tag)
		}
	}
}

func TestVueTemplateIdentifiersAreInScope(t *testing.T) {
	for _, f := range vueSFCFiles(t) {
		rel, script, tmpl := loadVueSFC(t, f)
		if tmpl == "" {
			continue
		}
		bound := vueScriptBindings(script)
		exprs := vueTemplateExprs(tmpl)
		if len(exprs) == 0 {
			t.Errorf("%s: no template expressions found — extractor broken", rel)
			continue
		}
		scope := vueScopeVars(exprs, tmpl)
		ok := func(id string) bool { return bound[id] || scope[id] || vueTemplateGlobals[id] }
		unresolved := map[string]string{} // id -> "call" or "reference"
		for _, e := range exprs {
			stripped := vueStripStrings(e.code)
			for _, m := range vueCallRE.FindAllStringSubmatch(stripped, -1) {
				if id := m[2]; !ok(id) {
					if _, dup := unresolved[id]; !dup {
						unresolved[id] = "call"
					}
				}
			}
			for _, id := range vueBareRefs(e.code) {
				if !ok(id) {
					if _, dup := unresolved[id]; !dup {
						unresolved[id] = "reference"
					}
				}
			}
		}
		ids := make([]string, 0, len(unresolved))
		for id := range unresolved {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			t.Errorf("%s: template %s %q is not bound in this SFC's setup scope", rel, unresolved[id], id)
		}
	}
}

// vueScriptDeclNames collects every name the script itself could reference —
// deliberately lenient (any indent, params, destructures, for/catch bindings):
// this test's job is catching a name used but NEVER declared, exactly what a
// careless split produces (UnionBlacklistSection wrote error.value with the
// ref left behind in UnionView; DashboardPage kept texPage.value = 1 after
// the page state moved into TexturesCard — both compile and blow up at
// runtime, the second one only after a successful upload).
func vueScriptDeclNames(script string) map[string]bool {
	d := vueScriptBindings(script)
	addList := func(list string) {
		for _, part := range strings.Split(list, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if i := strings.Index(part, "="); i >= 0 { // default values: name is the left side
				part = strings.TrimSpace(part[:i])
			}
			if vueWordRE.MatchString(part) {
				d[part] = true
			}
		}
	}
	for _, m := range vueDeclAnyRE.FindAllStringSubmatch(script, -1) {
		d[m[3]] = true
	}
	for _, m := range vueArrDestructRE.FindAllStringSubmatch(script, -1) {
		addList(m[2])
	}
	for _, m := range vueObjDestructRE.FindAllStringSubmatch(script, -1) {
		for _, part := range strings.Split(m[2], ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			if i := strings.Index(part, ":"); i >= 0 { // { src: dst } binds dst
				part = strings.TrimSpace(part[i+1:])
			}
			if i := strings.Index(part, "="); i >= 0 {
				part = strings.TrimSpace(part[:i])
			}
			if vueWordRE.MatchString(part) {
				d[part] = true
			}
		}
	}
	for _, m := range vueForVarRE.FindAllStringSubmatch(script, -1) {
		d[m[1]] = true
	}
	for _, m := range vueCatchRE.FindAllStringSubmatch(script, -1) {
		d[m[1]] = true
	}
	for _, m := range vueFnParamsRE.FindAllStringSubmatch(script, -1) {
		addList(m[1])
	}
	for _, m := range vueArrowParamsRE.FindAllStringSubmatch(script, -1) {
		addList(m[1])
	}
	for _, m := range vueArrowSingle.FindAllStringSubmatch(script, -1) {
		d[m[1]] = true
	}
	return d
}

func TestVueScriptValueUsersAreDeclared(t *testing.T) {
	for _, f := range vueSFCFiles(t) {
		rel, script, _ := loadVueSFC(t, f)
		declared := vueScriptDeclNames(script)
		seen := map[string]bool{}
		for _, m := range vueValueUserRE.FindAllStringSubmatch(script, -1) {
			name := m[2]
			if seen[name] || declared[name] {
				continue
			}
			seen[name] = true
			t.Errorf("%s: script reads %s.value but %s is never declared in this SFC — it throws at runtime the first time that line runs", rel, name, name)
		}
	}
}
