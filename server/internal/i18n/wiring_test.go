package i18n

// wiring_test.go — the error code's three homes must stay in step.
//
// A code lives in three places at once: the constant that declares it, the
// call site that raises it, and the two language packs that translate it.
// Two of those directions were already guarded (a raised code must be
// declared; the naming contract holds). What was not guarded is what happens
// when one side stops:
//
//   - declared but never raised: a dead constant nobody can see, kept forever
//     because removing it feels like deleting a promise;
//   - declared but untranslated: the panel falls back to the server's
//     message, which is English, so a Chinese operator reads English and
//     nobody notices — the code is "working";
//   - translated but undeclared: a pack entry for a code the envelope no
//     longer carries, which is invisible until somebody searches for it.
//
// All three are checked here, in both languages, in both directions.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// transportKeys are frontend-only failure kinds with no server code behind
// them (a dropped connection has no envelope). They live in the packs by
// design and are exempt from "must be declared".
var transportKeys = map[string]bool{"network": true, "networkTimeout": true, "unknown": true}

// retired lists codes that stay in the catalogue on purpose — a code is API
// surface, so it is retired by stopping use, never by redefining it — while
// no longer being raised anywhere. Keep it empty unless a removal is a
// deliberate, reviewed decision.
var retired = map[string]bool{}

func serverRoot(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..")
}

// declaredCodes reads both catalogues: the transport/admin namespace here, and
// the business codes the account service owns (they never collide — codes_test
// pins that).
func declaredCodes(t *testing.T) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, c := range stringConsts(t, "codes.go") {
		out[c.value] = "i18n." + c.name
	}
	for _, c := range stringConsts(t, filepath.Join("..", "account", "service.go")) {
		out[c.value] = "account." + c.name
	}
	if len(out) < 100 {
		t.Fatalf("declared %d codes, want both catalogues", len(out))
	}
	return out
}

// packKeys flattens an "errors" subtree into dot paths -> text.
func packKeys(t *testing.T, rel string) map[string]string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "web", "src", "locales", rel))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s is not JSON: %v", rel, err)
	}
	out := map[string]string{}
	var walk func(node any, prefix string)
	walk = func(node any, prefix string) {
		switch v := node.(type) {
		case map[string]any:
			for k, child := range v {
				next := k
				if prefix != "" {
					next = prefix + "." + k
				}
				walk(child, next)
			}
		case string:
			out[prefix] = v
		}
	}
	walk(doc["errors"], "")
	if len(out) < 100 {
		t.Fatalf("%s flattened to %d error keys, want the whole pack", rel, len(out))
	}
	return out
}

// Every code the backend can raise must have a sentence in both packs, and the
// sentence must not be blank — an empty translation reads as "no message".
func TestEveryDeclaredCodeIsTranslatedInBothPacks(t *testing.T) {
	codes := declaredCodes(t)
	for _, pack := range []string{"zh-CN.json", "en.json"} {
		keys := packKeys(t, pack)
		for code := range codes {
			text, ok := keys[code]
			if !ok {
				t.Errorf("%s: %s has no translation — the panel would fall back to the English server message", pack, code)
				continue
			}
			if strings.TrimSpace(text) == "" {
				t.Errorf("%s: %s translates to a blank line", pack, code)
			}
		}
	}
}

// The other direction: a translation for a code nothing declares is a promise
// nobody keeps, and it hides the moment somebody searches the catalogue for it.
func TestEveryTranslationHasADeclaredCode(t *testing.T) {
	codes := declaredCodes(t)
	for _, pack := range []string{"zh-CN.json", "en.json"} {
		for code := range packKeys(t, pack) {
			if transportKeys[code] {
				continue
			}
			if _, ok := codes[code]; !ok {
				t.Errorf("%s: errors.%s has no code behind it in i18n or account", pack, code)
			}
		}
	}
}

// A declared code that no call site raises is dead weight — and worse, it is
// indistinguishable from a code whose only uses were deleted by accident.
// References are counted outside the declaring file and outside tests: a code
// that only tests mention is not being raised.
func TestEveryDeclaredCodeIsRaisedSomewhere(t *testing.T) {
	root := serverRoot(t)
	files := map[string]string{} // path -> contents
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		files[p] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(files) < 30 {
		t.Fatalf("walked %d files, want the whole server tree", len(files))
	}

	for _, c := range stringConsts(t, "codes.go") {
		if retired[c.name] {
			continue
		}
		if !referencedIn(files, filepath.Join(root, "codes.go"), "i18n."+c.name) {
			t.Errorf("%s (%s) is declared but never raised", c.name, c.value)
		}
	}
	for _, c := range stringConsts(t, filepath.Join("..", "account", "service.go")) {
		if retired[c.name] {
			continue
		}
		if !referencedIn(files, filepath.Join(root, "account", "service.go"), c.name) {
			t.Errorf("account.%s (%s) is declared but never raised", c.name, c.value)
		}
	}
}

// referencedIn reports whether ident appears in any walked file other than
// skip. The identifier is matched with word boundaries so MsgInternal cannot
// be satisfied by MsgInternalSomething.
func referencedIn(files map[string]string, skip, ident string) bool {
	re := regexp.MustCompile(`\b` + regexp.QuoteMeta(ident) + `\b`)
	for p, body := range files {
		if filepath.Clean(p) == filepath.Clean(skip) {
			continue
		}
		if re.MatchString(body) {
			return true
		}
	}
	return false
}
