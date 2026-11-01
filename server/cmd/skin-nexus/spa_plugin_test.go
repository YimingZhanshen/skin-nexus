package main

import "testing"

func TestIsKnownSPAPathPluginUI(t *testing.T) {
	ui := func(p string) bool { return p == "ext/demo/page" }
	if !isKnownSPAPath("ext/demo/page", ui) {
		t.Fatal("declared plugin page should be SPA-known")
	}
	if isKnownSPAPath("ext/demo/missing", ui) {
		t.Fatal("undeclared plugin page must 404")
	}
	if !isKnownSPAPath("skinlib", nil) || isKnownSPAPath("nope", nil) {
		t.Fatal("built-in whitelist changed")
	}
}

// The paths we put in outgoing mail MUST be shell-known: an unlisted path 404s
// on the server before the SPA can load, which turns every emailed link into a
// dead end that no browser test of the API can see. Both of these were caught
// only by loading the real URL — hence the assertion.
func TestIsKnownSPAPathCoversEmailedLinks(t *testing.T) {
	for _, p := range []string{"reset-password", "email-verify"} {
		if !isKnownSPAPath(p, nil) {
			t.Fatalf("mailed link path %q is not SPA-known — the link in the mail would 404", p)
		}
	}
}
