// SkinNexus · Minecraft skin hosting platform.
// Copyright (C) 2026 SkinNexus contributors. Licensed under AGPL-3.0.
//
// tokens_map.go: the concrete-value view of a theme's root tokens, for consumers
// that cannot evaluate CSS (mail). TokenStyle keeps conditional blocks because
// a browser can; a letter has no media context, so the dark :root inside
// @media (prefers-color-scheme: dark) must NOT leak into the map — that is the
// one deliberate difference from ExtractRootTokens.

package theme

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// TokenMap returns the theme's root custom properties as concrete color values.
// Aliases resolve at most two hops; cycles, missing targets, alpha colors and
// modern color syntax are dropped rather than emitted, because a mail client
// renders none of them. Cached like TokenStyle; a rescan replaces the *Theme.
func (t *Theme) TokenMap() map[string]string {
	t.tokensMapOnce.Do(func() { t.tokensMap = buildTokenMap(t) })
	return t.tokensMap
}

func buildTokenMap(t *Theme) map[string]string {
	raw := map[string]string{}
	for _, f := range t.Manifest.Inject.CSS {
		if !fileRe.MatchString(f) {
			continue
		}
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
		for k, v := range rootTokenDecls(string(src)) {
			raw[k] = v // later definition wins, like the cascade
		}
	}
	out := map[string]string{}
	for k, v := range raw {
		if resolved, ok := resolveTokenAlias(raw, k, v, 0, map[string]bool{}); ok {
			if c := usableTokenColor(resolved); c != "" {
				out[k] = c
			}
		}
	}
	return out
}

// rootTokenDecls collects --* declarations from unconditional :root blocks only.
func rootTokenDecls(css string) map[string]string {
	out := map[string]string{}
	for _, e := range cssEntries(stripCSSComments(css)) {
		if strings.HasPrefix(e.prelude, "@") {
			continue // no media context in a letter: skip dark/light switches
		}
		if !isRootSelector(e.prelude) {
			continue
		}
		for _, decl := range splitDeclarations(e.body) {
			name, value, ok := strings.Cut(decl, ":")
			if !ok {
				continue
			}
			name, value = strings.TrimSpace(name), strings.TrimSpace(value)
			if !strings.HasPrefix(name, "--") || value == "" {
				continue
			}
			if strings.Contains(name, "</") || strings.Contains(value, "</") {
				continue
			}
			out[name] = value
		}
	}
	return out
}

// resolveTokenAlias follows var(--x[, fallback]) up to two hops. A cycle or a
// missing target without a fallback drops the token; the caller then falls back
// to its own constant instead of shipping an unrenderable value.
func resolveTokenAlias(raw map[string]string, name, value string, depth int, seen map[string]bool) (string, bool) {
	if !strings.HasPrefix(value, "var(") || !strings.HasSuffix(value, ")") {
		return value, true
	}
	if depth >= 2 || seen[name] {
		return "", false
	}
	seen[name] = true
	inner := value[4 : len(value)-1]
	target, fallback, hasFallback := strings.Cut(inner, ",")
	target = strings.TrimSpace(target)
	fallback = strings.TrimSpace(fallback)
	if next, ok := raw[target]; ok {
		return resolveTokenAlias(raw, target, next, depth+1, seen)
	}
	if hasFallback {
		return resolveTokenAlias(raw, name, fallback, depth+1, seen)
	}
	return "", false
}

// usableTokenColor accepts 3/6-digit hex and integer rgb()/rgba(alpha=1) only.
func usableTokenColor(v string) string {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, "#") {
		h := v[1:]
		if len(h) != 3 && len(h) != 6 {
			return ""
		}
		for _, c := range h {
			if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
				return ""
			}
		}
		return strings.ToLower(v)
	}
	for _, prefix := range []string{"rgb(", "rgba("} {
		if !strings.HasPrefix(v, prefix) || !strings.HasSuffix(v, ")") {
			continue
		}
		parts := strings.Split(v[len(prefix):len(v)-1], ",")
		if len(parts) != 3 && len(parts) != 4 {
			return ""
		}
		var nums [3]int
		for i := 0; i < 3; i++ {
			n, err := strconv.Atoi(strings.TrimSpace(parts[i]))
			if err != nil || n < 0 || n > 255 {
				return ""
			}
			nums[i] = n
		}
		if len(parts) == 4 {
			a := strings.TrimSpace(parts[3])
			if a != "1" && a != "1.0" {
				return "" // semi-transparent band over an unknown client background
			}
		}
		return "rgb(" + strconv.Itoa(nums[0]) + "," + strconv.Itoa(nums[1]) + "," + strconv.Itoa(nums[2]) + ")"
	}
	return ""
}
