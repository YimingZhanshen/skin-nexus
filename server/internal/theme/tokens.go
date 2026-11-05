// SkinNexus · Minecraft skin hosting platform.
// Copyright (C) 2026 SkinNexus contributors. Licensed under AGPL-3.0.
//
// tokens.go: the token-only view of a theme stylesheet.

package theme

import (
	"os"
	"path/filepath"
	"strings"
)

// maxTokenSourceBytes bounds one stylesheet read for the token view: a pack
// whose CSS is bigger than this is a bundle, and parsing a truncated tail is
// worse than emitting nothing.
const maxTokenSourceBytes = 2 << 20

// TokenStyle renders the theme's root tokens as one `<style>` block, for
// documents that should inherit the palette but not the host's element rules.
//
// Plugin UI pages live in a sandboxed iframe with their own markup. Linking
// the pack's real stylesheet there also applies `body{background-image:…}`,
// `h1,h2{font-family:…}` and `input{font-size:…}` to that document, so a
// plugin page arrived wearing the site's decorative wash and typography instead
// of its own (measured on wzbc-violet, 2026-11-05). Extracted root tokens give
// the same palette with none of that.
//
// An empty result means "no root tokens found" and is the caller's signal to
// inject nothing: the plugin keeps its own defaults and the JS bridge still
// delivers theme, locale and user state.
func (t *Theme) TokenStyle() string {
	t.tokensOnce.Do(func() { t.tokensCSS = buildTokenStyle(t) })
	return t.tokensCSS
}

func buildTokenStyle(t *Theme) string {
	var body strings.Builder
	for _, f := range t.Manifest.Inject.CSS {
		if !fileRe.MatchString(f) {
			continue
		}
		// Same containment discipline as Registry.ResolveAsset: the file list
		// comes from a pack's theme.json, which is third-party input.
		full := filepath.Join(t.BaseDir, f)
		if !strings.HasPrefix(full, t.BaseDir+string(os.PathSeparator)) {
			continue
		}
		st, err := os.Stat(full)
		if err != nil || st.IsDir() || st.Size() > maxTokenSourceBytes {
			continue
		}
		src, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		if toks := ExtractRootTokens(string(src)); toks != "" {
			body.WriteString(toks)
			body.WriteString("\n")
		}
	}
	if body.Len() == 0 {
		return ""
	}
	return `<style id="skin-theme-tokens">` + body.String() + `</style>` + "\n"
}

// ExtractRootTokens keeps only the custom properties (and color-scheme) a
// theme declares on the root selector.
//
// A theme stylesheet is written for the host's DOM: alongside its `:root`
// tokens it carries bare element rules (body{background-image}, h1,h2{
// font-family}, input{font-size}, th,td{border-bottom}). Plugin pages have
// their own markup, so linking the whole file into a plugin document restyles
// that markup — the palette arrives together with the site's typography and
// decorative washes. Tokens are the only part a plugin document should inherit.
//
// Only a selector list made entirely of :root / html qualifies, and only
// `--*` declarations plus color-scheme survive: anything else on the root
// element (scrollbar-color, overflow) is still a host-DOM decision.
func ExtractRootTokens(css string) string {
	css = stripCSSComments(css)
	var blocks []string
	for _, e := range cssEntries(css) {
		if strings.HasPrefix(e.prelude, "@") {
			// Conditional group rules keep the palette switch (a theme may
			// define --bg twice, once per prefers-color-scheme); everything
			// else (@font-face, @keyframes, @page) is host-DOM decoration.
			if !isConditionalAtRule(e.prelude) {
				continue
			}
			if inner := ExtractRootTokens(e.body); inner != "" {
				blocks = append(blocks, e.prelude+"{\n"+inner+"\n}")
			}
			continue
		}
		if !isRootSelector(e.prelude) {
			continue
		}
		if decls := filterRootDecls(e.body); decls != "" {
			blocks = append(blocks, ":root{"+decls+"}")
		}
	}
	if len(blocks) == 0 {
		return ""
	}
	return strings.Join(blocks, "\n")
}

func isConditionalAtRule(prelude string) bool {
	if strings.Contains(prelude, "</") {
		return false
	}
	name := ""
	for _, f := range strings.Fields(prelude) {
		name = f
		break
	}
	switch strings.ToLower(name) {
	case "@media", "@supports", "@layer", "@container":
		return true
	}
	return false
}

func isRootSelector(prelude string) bool {
	for _, sel := range strings.Split(prelude, ",") {
		switch strings.ToLower(strings.TrimSpace(sel)) {
		case ":root", "html":
		default:
			return false
		}
	}
	return true
}

func filterRootDecls(body string) string {
	var kept []string
	for _, decl := range splitDeclarations(body) {
		name, value, ok := strings.Cut(decl, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		// The fragment is inlined inside <style>, so a value carrying "</"
		// would close the element and let the rest of the file out as markup.
		// Dropping that declaration is cheaper than escaping it: no legitimate
		// token value needs the sequence.
		if strings.Contains(name, "</") || strings.Contains(value, "</") {
			continue
		}
		lower := strings.ToLower(name)
		if strings.HasPrefix(lower, "--") || lower == "color-scheme" {
			kept = append(kept, name+": "+value+";")
		}
	}
	return strings.Join(kept, " ")
}

type cssEntry struct {
	prelude string
	body    string
}

// cssEntries splits a stylesheet into top-level (prelude, block body) pairs,
// ignoring braces inside strings and comments. Nested blocks stay in the body
// so a caller can recurse (see the @media handling).
func cssEntries(css string) []cssEntry {
	var entries []cssEntry
	i, preludeStart := 0, 0
	for i < len(css) {
		switch c := css[i]; {
		case c == '/' && i+1 < len(css) && css[i+1] == '*':
			i = skipComment(css, i)
		case c == '"' || c == '\'':
			i = skipString(css, i)
		case c == '{':
			prelude := strings.TrimSpace(css[preludeStart:i])
			bodyStart := i + 1
			j := matchingBrace(css, bodyStart)
			entries = append(entries, cssEntry{prelude: prelude, body: css[bodyStart:j]})
			i, preludeStart = j+1, j+1
		case c == ';':
			i, preludeStart = i+1, i+1
		default:
			i++
		}
	}
	return entries
}

// matchingBrace returns the index of the } that closes a block whose body
// starts at from. Unclosed input yields len(css), so callers never read past it.
func matchingBrace(css string, from int) int {
	depth, i := 1, from
	for i < len(css) {
		switch c := css[i]; {
		case c == '/' && i+1 < len(css) && css[i+1] == '*':
			i = skipComment(css, i)
		case c == '"' || c == '\'':
			i = skipString(css, i)
		case c == '{':
			depth++
			i++
		case c == '}':
			if depth--; depth == 0 {
				return i
			}
			i++
		default:
			i++
		}
	}
	return len(css)
}

// splitDeclarations cuts a block body on semicolons that are not inside a
// string, comment or function parentheses (url(data:…;…) is legal CSS).
func splitDeclarations(body string) []string {
	var out []string
	start, depth := 0, 0
	for i := 0; i < len(body); {
		switch c := body[i]; {
		case c == '/' && i+1 < len(body) && body[i+1] == '*':
			i = skipComment(body, i)
		case c == '"' || c == '\'':
			i = skipString(body, i)
		case c == '(':
			depth++
			i++
		case c == ')':
			if depth > 0 {
				depth--
			}
			i++
		case c == ';' && depth == 0:
			if decl := strings.TrimSpace(body[start:i]); decl != "" {
				out = append(out, decl)
			}
			i++
			start = i
		default:
			i++
		}
	}
	if rest := strings.TrimSpace(body[start:]); rest != "" {
		out = append(out, rest)
	}
	return out
}

func skipComment(s string, i int) int {
	j := i + 2
	for j < len(s) {
		if s[j] == '*' && j+1 < len(s) && s[j+1] == '/' {
			return j + 2
		}
		j++
	}
	return len(s)
}

func skipString(s string, i int) int {
	quote := s[i]
	j := i + 1
	for j < len(s) {
		switch s[j] {
		case '\\':
			j += 2
			continue
		case quote:
			return j + 1
		}
		j++
	}
	return len(s)
}

// stripCSSComments removes /* */ spans that are not inside a string.
//
// Comments are not part of a selector or a property name, so leaving their
// bytes in the scanned text decides behaviour by accident: a `:root` block
// introduced by a comment stops matching the root selector, and a token written
// as `--bg: #fff; /* paper */` stops being a `--*` declaration. wzbc-violet is
// commented exactly that way, which silently dropped its whole palette.
func stripCSSComments(css string) string {
	var sb strings.Builder
	sb.Grow(len(css))
	last := byte('\n')
	for i := 0; i < len(css); {
		switch c := css[i]; {
		case c == '/' && i+1 < len(css) && css[i+1] == '*':
			// Keep a separator so `a/* x */b` does not fuse into `ab`.
			if sb.Len() > 0 && last != ' ' && last != '\t' && last != '\n' {
				sb.WriteByte(' ')
			}
			i = skipComment(css, i)
		case c == '"' || c == '\'':
			j := skipString(css, i)
			sb.WriteString(css[i:j])
			last, i = css[j-1], j
		default:
			sb.WriteByte(c)
			last, i = c, i+1
		}
	}
	return sb.String()
}
