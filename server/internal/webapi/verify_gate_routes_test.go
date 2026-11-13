package webapi

// verify_gate_routes_test.go — the exemption list cannot grow by accident.
//
// The web gate is deny-by-default over /api/v1, which means the only way to
// weaken it is to add a line to webGateOpenRoutes. That is also the easiest
// line to add lazily: a new route 403s for a pending test account, and the
// fastest "fix" during a session looks like another exemption. This test makes
// that a deliberate edit with three teeth: a stale exemption, a widened list,
// and an exemption that cannot work (a wildcard path, which the matcher
// compares by exact string) all fail here.

import (
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// handleRouteRE picks route registrations out of Handler(). It matches the
// same literal form every registration in this package uses.
var handleRouteRE = regexp.MustCompile(`mux\.HandleFunc\("([A-Z]+ [^"]+)"`)

func registeredRoutes(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("webapi.go")
	if err != nil {
		t.Fatalf("read webapi.go: %v", err)
	}
	var out []string
	for _, m := range handleRouteRE.FindAllStringSubmatch(string(src), -1) {
		out = append(out, m[1])
	}
	if len(out) < 30 {
		t.Fatalf("found %d route registrations; the extractor is broken and this gate would pass on nothing", len(out))
	}
	return out
}

func TestWebGateExemptionListIsLiveAndDeclared(t *testing.T) {
	registered := map[string]bool{}
	for _, r := range registeredRoutes(t) {
		registered[r] = true
	}

	for key, reason := range webGateOpenRoutes {
		if !registered[key] {
			t.Fatalf("%s is exempt from the gate but no such route is registered — stale exemptions are how a removed route comes back ungated", key)
		}
		if strings.ContainsAny(key, "{") {
			t.Fatalf("%s cannot be exempt: the matcher compares exact paths, so a wildcard entry silently exempts nothing", key)
		}
		if len(strings.TrimSpace(reason)) < 20 {
			t.Fatalf("%s has no real reason on file (%q): an exemption without a reason is a hole with a comment", key, reason)
		}
	}

	// The families the gate exists to cover must be on the blocked side. Named
	// here rather than derived, so removing a whole family from the mux (or
	// exempting it) is a visible change instead of a passing test.
	for _, must := range []string{
		"POST /api/v1/me/textures",
		"DELETE /api/v1/me/textures/{tid}",
		"POST /api/v1/me/players",
		"PUT /api/v1/me/players/{id}/skin",
		"GET /api/v1/me/closet",
		"POST /api/v1/me/closet",
		"GET /api/v1/me/notifications",
		"GET /api/v1/me/reports",
		"POST /api/v1/skinlib/{tid}/like",
		"POST /api/v1/skinlib/{tid}/report",
		"POST /api/v1/union/bind",
		"POST /api/v1/me/password",
	} {
		if !registered[must] {
			t.Fatalf("%s is no longer registered — this list needs to be re-read, not deleted", must)
		}
		if _, exempt := webGateOpenRoutes[must]; exempt {
			t.Fatalf("%s became exempt from the gate: that is the account acting as itself, which is what the gate refuses", must)
		}
	}
}

// The exemption list is the contract. Adding to it must be a decision someone
// can see in a diff, so the whole set is pinned here as well as explained in
// the source.
func TestWebGateExemptionSetIsExactlyTheAgreedWaysOut(t *testing.T) {
	got := make([]string, 0, len(webGateOpenRoutes))
	for k := range webGateOpenRoutes {
		got = append(got, k)
	}
	sort.Strings(got)

	want := []string{
		"DELETE /api/v1/me",
		"GET /api/v1/announcement",
		"GET /api/v1/me",
		"GET /api/v1/plugins/ui",
		"GET /api/v1/setup/status",
		"GET /api/v1/site-config",
		"GET /api/v1/skinlib",
		"POST /api/v1/email/confirm",
		"POST /api/v1/email/verify",
		"POST /api/v1/login",
		"POST /api/v1/logout",
		"POST /api/v1/password/forgot",
		"POST /api/v1/password/reset",
		"POST /api/v1/register",
		"POST /api/v1/setup",
		"PUT /api/v1/me/profile",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("exemption set changed:\n got: %s\nwant: %s", strings.Join(got, "; "), strings.Join(want, "; "))
	}
}
