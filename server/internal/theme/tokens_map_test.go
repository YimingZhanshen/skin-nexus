package theme

import (
	"os"
	"path/filepath"
	"testing"
)

func tokenTheme(t *testing.T, css string) *Theme {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "theme.css"), []byte(css), 0o644); err != nil {
		t.Fatal(err)
	}
	return &Theme{Manifest: Manifest{Inject: Inject{CSS: []string{"theme.css"}}}, BaseDir: dir}
}

// The semantics table from the plan: mail has no media context and no CSS
// engine, so only concrete, opaque, universally-renderable colors survive.
func TestTokenMapSemantics(t *testing.T) {
	css := `:root {
  --accent: #b0453a;
  --short: #abc;
  --rgb: rgb(176, 69, 58);
  --one: var(--accent);
  --two: var(--one);
  --three: var(--two);
  --missing: var(--nope);
  --fallback: var(--nope, #123456);
  --alpha: rgba(169,138,95,.24);
  --opaque: rgba(1,2,3,1);
  --hex8: #11223344;
  --modern: oklch(0.7 0.1 20);
  --named: red;
  --grad: linear-gradient(#fff, #000);
	}
	@media (prefers-color-scheme: dark) { :root { --accent: #ffffff; --darkonly: #101825; } }
	:root { --accent: #2266aa; }`
	m := tokenTheme(t, css).TokenMap()
	want := map[string]string{
		"--accent":   "#2266aa", // later definition wins, dark block ignored
		"--short":    "#abc",
		"--rgb":      "rgb(176,69,58)",
		"--one":      "#2266aa",
		"--two":      "#2266aa",
		"--fallback": "#123456",
		"--opaque":   "rgb(1,2,3)",
	}
	for k, v := range want {
		if m[k] != v {
			t.Errorf("TokenMap[%s] = %q, want %q", k, m[k], v)
		}
	}
	for _, k := range []string{"--three", "--missing", "--alpha", "--hex8", "--modern", "--named", "--grad", "--darkonly"} {
		if _, ok := m[k]; ok {
			t.Errorf("TokenMap must drop %s (got %q): unrenderable or media-scoped", k, m[k])
		}
	}
	for k, v := range m {
		if len(v) > 4 && v[:4] == "var(" {
			t.Errorf("TokenMap[%s] leaked an unresolved var(): %q", k, v)
		}
	}
}

func TestTokenMapCycleDropsBoth(t *testing.T) {
	m := tokenTheme(t, `:root { --a: var(--b); --b: var(--a); }`).TokenMap()
	if len(m) != 0 {
		t.Errorf("a cycle must drop both tokens, got %v", m)
	}
}

func TestTokenMapEmptyIsNotNil(t *testing.T) {
	m := tokenTheme(t, `body { color: red; }`).TokenMap()
	if m == nil {
		t.Error("no root tokens must yield an empty map, not nil (callers range over it)")
	}
	if len(m) != 0 {
		t.Errorf("element rules are not tokens, got %v", m)
	}
}

func TestTokenMapInjectionValueDropped(t *testing.T) {
	m := tokenTheme(t, `:root { --x: #112233 </style><script>alert(1)</script>; --ok: #abc; }`).TokenMap()
	if _, ok := m["--x"]; ok {
		t.Error("a value carrying </ must be dropped, not escaped into a letter")
	}
	if m["--ok"] != "#abc" {
		t.Errorf("the clean sibling token must survive, got %v", m)
	}
}
