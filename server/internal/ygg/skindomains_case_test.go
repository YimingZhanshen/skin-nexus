package ygg

// skindomains_case_test.go — W1-2, the case half of auth-server URL
// normalisation.
//
// authlib-injector decides whether a texture host is trusted with
// String.equals / endsWith / startsWith (SkinWhitelistTransformUnit
// .domainMatches, refs/authlib-injector .../SkinWhitelistTransformUnit.java:65-69)
// and reads the host out of the texture URL with new URI(url).getHost(), which
// keeps whatever case the URL was written with (same file, :94). So the entry
// we publish and the texture URL must agree CHARACTER FOR CHARACTER: a
// configuration typed as "Example.COM" and a texture served from
// "https://example.com/..." match nothing, the account still loads, and the
// skin silently does not appear.
//
// The fix under test is additive rather than a fold: every entry is published
// exactly as configured (the G10 passthrough pin) AND, when its lowercase form
// differs, in lowercase too. A fold would drop the spelling a mixed-case
// texture URL — one this station does not build, e.g. a member station's —
// matches today, so folding can only ever remove working matches, while the
// pair below can only add them.

import (
	"net/http/httptest"
	"testing"
)

// caseDomains builds the published list for one configuration, isolating the
// static formula the way domainsFor does.
func caseDomains(t *testing.T, configured []string, homepage, reqHost string, extra []string) []string {
	t.Helper()
	api, _ := newTestAPI(t)
	api.cfg.SkinDomains = configured
	api.cfg.Homepage = homepage
	if extra != nil {
		api.cfg.ExtraSkinDomains = func() []string { return extra }
	} else {
		api.cfg.ExtraSkinDomains = nil
	}
	r := httptest.NewRequest("GET", "/api/yggdrasil/", nil)
	r.Host = reqHost
	return api.skinDomains(r)
}

// Every source of a whitelist entry must contribute both spellings: the
// operator-typed configuration, the site_url host, the request Host header and
// the union member hosts all reach the same emitter.
func TestSkinDomainsPublishesLowercaseSpelling(t *testing.T) {
	cases := []struct {
		name       string
		configured []string
		homepage   string
		reqHost    string
		extra      []string
		original   string
		lower      string
	}{
		{"configured entry", []string{"EXAMPLE.INVALID"}, "", "h.example", nil, "EXAMPLE.INVALID", "example.invalid"},
		{"site_url host", nil, "https://Site.Invalid/", "h.example", nil, "Site.Invalid", "site.invalid"},
		{"request host", nil, "", "REQ.Invalid", nil, "REQ.Invalid", "req.invalid"},
		{"union member host", nil, "", "h.example", []string{"MEMBER.Invalid"}, "MEMBER.Invalid", "member.invalid"},
		{"leading dot keeps its suffix meaning", []string{".SKIN.INVALID"}, "", "h.example", nil, ".SKIN.INVALID", ".skin.invalid"},
		{"port survives on both spellings", []string{"SITE.INVALID:8443"}, "", "h.example", nil, "SITE.INVALID:8443", "site.invalid:8443"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := caseDomains(t, c.configured, c.homepage, c.reqHost, c.extra)
			if countOf(got, c.original) != 1 {
				t.Fatalf("the configured spelling must still be published verbatim exactly once: %v", got)
			}
			if countOf(got, c.lower) != 1 {
				t.Fatalf("lowercase spelling %q must be published exactly once — the client compares case-sensitively: %v", c.lower, got)
			}
		})
	}
}

// The two spellings must not multiply: when three sources already agree on the
// lowercase form it is published once, and the portless twin derived from a
// mixed-case entry lands in lowercase exactly once too.
func TestSkinDomainsCaseSpellingsDoNotMultiply(t *testing.T) {
	got := caseDomains(t, []string{"Example.invalid", "example.invalid"}, "https://example.invalid", "h.example", nil)
	if n := countOf(got, "example.invalid"); n != 1 {
		t.Fatalf("the lowercase spelling must appear exactly once across all sources, got %d: %v", n, got)
	}
	if n := countOf(got, "Example.invalid"); n != 1 {
		t.Fatalf("the configured spelling must appear exactly once, got %d: %v", n, got)
	}
}

// Equivalence, stated the way the client experiences it: two configurations
// that differ only in the case of the host must both be able to match a
// texture served from the lowercase host. The set of spellings each one
// publishes therefore has to contain the lowercase host.
func TestSkinDomainsCaseVariantsMatchTheSameTextureHost(t *testing.T) {
	for _, configured := range []string{"Skin.Example.invalid", "skin.example.invalid", "SKIN.EXAMPLE.INVALID"} {
		got := caseDomains(t, []string{configured}, "", "h.example", nil)
		if countOf(got, "skin.example.invalid") != 1 {
			t.Fatalf("configuration %q must still cover the lowercase texture host, got %v", configured, got)
		}
	}
}
