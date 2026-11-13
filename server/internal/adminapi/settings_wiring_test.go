package adminapi

// settings_wiring_test.go — the panel's settings screen and the whitelist it
// renders are two halves of one surface, written in two languages.
//
// The screen is API-driven: it draws whatever handleSettingsGet returns, so
// the rendered key set can never drift from settingsWhitelist. What CAN drift
// are the two things the frontend hardcodes on top of that payload:
//
//   - boolKeys / textKeys decide which widget a key gets. A key added to the
//     whitelist but forgotten here silently falls through to a plain text
//     input — an operator typing "true" into what should be a toggle, with no
//     error anywhere. A key spelled differently here than in the whitelist is
//     worse: the widget never matches and the fallback is invisible.
//   - GROUP_ORDER decides section order and titles. A group id the backend
//     gains without this list lands at the end under its raw identifier,
//     because the title lookup falls back to the id itself.
//
// Both directions are checked: no frontend key without a whitelist entry, no
// whitelist group without a place in the order.

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

const settingsViewPath = "../../../web/src/components/admin/SettingsView.vue"

// whitelistGroups returns every group id declared in settingsWhitelist.
func whitelistGroups(t *testing.T) map[string]bool {
	t.Helper()
	groups := map[string]bool{}
	for _, s := range settingsWhitelist {
		groups[s.group] = true
	}
	if len(groups) == 0 {
		t.Fatal("settingsWhitelist is empty — the extractor is looking at the wrong table")
	}
	return groups
}

func whitelistKeys(t *testing.T) map[string]bool {
	t.Helper()
	keys := map[string]bool{}
	for _, s := range settingsWhitelist {
		keys[s.key] = true
	}
	return keys
}

// vueSet extracts the string literals of `name = new Set([...])` or
// `name = [...]` from the settings component.
func vueSet(t *testing.T, body, name string) []string {
	t.Helper()
	start := regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\s*=\s*(new Set\()?\[`).FindStringIndex(body)
	if start == nil {
		t.Fatalf("%s not found in %s — the component renamed it, so this gate must be updated with it", name, settingsViewPath)
	}
	depth, i := 0, start[1]-1
	for ; i < len(body); i++ {
		switch body[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				goto found
			}
		}
	}
found:
	lit := regexp.MustCompile(`'([^']+)'`).FindAllStringSubmatch(body[start[1]:i+1], -1)
	if len(lit) == 0 {
		t.Fatalf("%s has no string literals — the extractor is broken", name)
	}
	out := make([]string, 0, len(lit))
	for _, m := range lit {
		out = append(out, m[1])
	}
	return out
}

func TestPanelKeySetsStayInsideTheWhitelist(t *testing.T) {
	src, err := os.ReadFile(settingsViewPath)
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	keys := whitelistKeys(t)

	for _, set := range []string{"boolKeys", "textKeys"} {
		for _, k := range vueSet(t, body, set) {
			if !keys[k] {
				t.Errorf("%s names %q, which settingsWhitelist does not carry — the widget would never match and the key is dead in the panel", set, k)
			}
		}
	}
}

func TestEveryWhitelistGroupHasAPlaceInThePanelOrder(t *testing.T) {
	src, err := os.ReadFile(settingsViewPath)
	if err != nil {
		t.Fatal(err)
	}
	order := vueSet(t, string(src), "GROUP_ORDER")
	inOrder := map[string]bool{}
	for _, g := range order {
		inOrder[g] = true
	}
	for g := range whitelistGroups(t) {
		if !inOrder[g] {
			t.Errorf("group %q is used by settingsWhitelist but missing from GROUP_ORDER — it would render at the end under its raw id", g)
		}
	}
}

// The extraction itself must be anchored: a gate that silently reads nothing
// would pass forever.
func TestSettingsWiringExtractorSeesThePayload(t *testing.T) {
	src, err := os.ReadFile(settingsViewPath)
	if err != nil {
		t.Fatal(err)
	}
	body := string(src)
	if !strings.Contains(body, "boolKeys") || !strings.Contains(body, "GROUP_ORDER") {
		t.Fatal("expected widget-routing sets in the settings component")
	}
	if len(whitelistKeys(t)) < 20 {
		t.Fatalf("settingsWhitelist yielded %d keys, expected the full table", len(whitelistKeys(t)))
	}
}
