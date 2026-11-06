package main

// landing_facts_test.go: the guard rails on the answer-first fact block added
// to the landing page (AEO, 2026-11-08).
//
// The block exists because AI answer engines and crawlers quote text that sits
// in the response, plainly, near the top. So every assertion here reads the
// rendered HTML bytes: nothing boots a browser, nothing runs JavaScript, and
// nothing asks the catalog for copy the template does not print. Three
// regressions matter and each has its own assertion:
//
//   - the block disappearing or going empty (a call site that forgets to fill
//     Facts, a template edit that drops the range);
//   - copy leaking as a bare catalog key (a missing entry renders the key
//     itself into a crawlable page — there is no vue-i18n fallback here);
//   - the language mixing, or someone answering an AEO ticket with schema
//     markup instead of prose.

import (
	"html/template"
	"regexp"
	"sort"
	"strings"
	"testing"

	"skin-nexus/server/internal/webi18n"
)

// factSectionStart is the element the block renders as. It is asserted as a
// literal, not derived from landing.go, so renaming the section fails here
// instead of silently skipping the checks.
const factSectionStart = `<section class="facts">`

// wantFacts is the size of the block as specified by the ticket — at most five
// answer-first pairs. It is written out here rather than derived from factKeys,
// so adding, dropping or forgetting a fact fails a test instead of quietly
// moving the goalpost.
const wantFacts = 5

// landingJSONLDTypesBefore is the @type set landing.go emitted on the day the
// fact block landed — measured by rendering both languages and collecting
// every `"@type"` occurrence: the template carries none (the WebSite /
// Organization block comes from the SEO engine after the template runs).
// Google states AI search needs no special schema, so the block ships prose
// only and this set stays empty.
var landingJSONLDTypesBefore = []string(nil)

var (
	jsonldTypeRE   = regexp.MustCompile(`"@type"\s*:\s*"([^"]*)"`)
	factItemRE     = regexp.MustCompile(`(?s)<div class="fact">.*?</div>`)
	dtRE           = regexp.MustCompile(`<dt>(.*?)</dt>`)
	ddRE           = regexp.MustCompile(`<dd>(.*?)</dd>`)
	scriptBlockRE  = regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`)
	hanRE          = regexp.MustCompile(`\p{Han}`)
	bareCatalogRE  = regexp.MustCompile(`\b(?:landing|detail|lib|page|ui|seo|viewer|report|notfound|edit|device)\.[A-Za-z0-9_]+`)
	factsHeadingRE = regexp.MustCompile(`(?s)<section class="facts">\s*<h2>(.*?)</h2>`)
)

// factsBlock returns the fact section as the server wrote it, failing the test
// when it is absent or unterminated.
func factsBlock(t *testing.T, html string) string {
	t.Helper()
	start := strings.Index(html, factSectionStart)
	if start < 0 {
		t.Fatalf("fact block %q is missing from the response", factSectionStart)
	}
	end := strings.Index(html[start:], "</section>")
	if end < 0 {
		t.Fatal("fact block is never closed")
	}
	return html[start : start+end]
}

// wantText is how a catalog string reaches the page: html/template escapes it
// in text context, so the expectation goes through the same escape instead of
// assuming the copy is quote-free.
func wantText(s string) string { return template.HTMLEscapeString(s) }

// TestLandingFactsAreServerRendered is the main gate: for both languages the
// block must sit in <body>, outside any <script>, and carry every question and
// answer of that language, exactly wantFacts times.
func TestLandingFactsAreServerRendered(t *testing.T) {
	for _, lang := range []string{webi18n.ZhCN, webi18n.En, ""} {
		lang := lang
		name := lang
		if name == "" {
			name = "fallback"
		}
		t.Run(name, func(t *testing.T) {
			html := renderLandingLang(t, lang)
			body := strings.Index(html, "<body")
			if body < 0 {
				t.Fatal("no <body> in the render")
			}
			blockAt := strings.Index(html, factSectionStart)
			if blockAt < body {
				t.Error("fact block sits outside <body>")
			}
			if end := strings.LastIndex(html, "</body>"); end >= 0 && blockAt > end {
				t.Error("fact block sits after </body>")
			}
			for _, s := range scriptBlockRE.FindAllString(html, -1) {
				if strings.Contains(s, `class="facts"`) {
					t.Fatal("the fact block is inside a <script>: it would not be crawlable copy")
				}
			}
			// Order is part of the point: answers come after the intro, which is
			// what "near the top, plainly" means for an answer engine.
			if intro := strings.Index(html, `<section class="intro">`); intro >= 0 && intro > blockAt {
				t.Error("fact block renders before the intro paragraph")
			}

			block := factsBlock(t, html)
			items := factItemRE.FindAllString(block, -1)
			if len(items) != wantFacts {
				t.Fatalf("fact block rendered %d pairs, want exactly %d (an empty render means Facts was not filled)", len(items), wantFacts)
			}
			effective := lang
			if effective == "" {
				effective = webi18n.ZhCN // what T() falls back to
			}
			for i, f := range landingFacts(effective) {
				if f.Q == "" || f.A == "" {
					t.Errorf("fact %d of %s has empty copy", i, effective)
					continue
				}
				if !strings.Contains(block, wantText(f.Q)) {
					t.Errorf("fact %d question of %s is not in the rendered block", i, effective)
				}
				if !strings.Contains(block, wantText(f.A)) {
					t.Errorf("fact %d answer of %s is not in the rendered block", i, effective)
				}
			}
			if h := factsHeadingRE.FindStringSubmatch(block); len(h) != 2 ||
				h[1] != wantText(webi18n.T(effective, "landing.factsTitle")) {
				t.Errorf("fact block heading is %v, want the %s facts title", h, effective)
			}
			// dt/dd must pair up: an answer engine reads the pair, not the count.
			if n := len(dtRE.FindAllString(block, -1)); n != wantFacts {
				t.Errorf("block carries %d <dt> questions, want %d", n, wantFacts)
			}
			if n := len(ddRE.FindAllString(block, -1)); n != wantFacts {
				t.Errorf("block carries %d <dd> answers, want %d", n, wantFacts)
			}
		})
	}
}

// A missing catalog entry renders its own key on the page — the SSR path has no
// fallback locale. This fails on any key reaching the HTML, in either language.
func TestLandingFactsLeakNoCatalogKeys(t *testing.T) {
	for _, lang := range []string{webi18n.ZhCN, webi18n.En} {
		html := renderLandingLang(t, lang)
		block := factsBlock(t, html)
		for _, m := range bareCatalogRE.FindAllString(block, -1) {
			t.Errorf("%s fact block prints the catalog key %q instead of copy", lang, m)
		}
		if strings.Contains(block, "landing.") {
			t.Errorf("%s fact block contains a bare landing.* key", lang)
		}
	}
}

// Each language must show its own answers, and only its own: Chinese copy in an
// English render is invisible to whoever wrote it and visible to every crawler.
func TestLandingFactsArePerLanguage(t *testing.T) {
	zh := factsBlock(t, renderLandingLang(t, webi18n.ZhCN))
	en := factsBlock(t, renderLandingLang(t, webi18n.En))
	if zh == en {
		t.Fatal("both languages rendered the same fact block")
	}
	for i, f := range landingFacts(webi18n.En) {
		if !strings.Contains(en, wantText(f.Q)) || !strings.Contains(en, wantText(f.A)) {
			t.Errorf("english fact %d is missing from the english block", i)
		}
		if strings.Contains(zh, wantText(f.Q)) {
			t.Errorf("english fact %d leaked into the chinese block", i)
		}
	}
	for i, f := range landingFacts(webi18n.ZhCN) {
		if strings.Contains(en, wantText(f.Q)) || strings.Contains(en, wantText(f.A)) {
			t.Errorf("chinese fact %d leaked into the english block", i)
		}
	}
	if m := hanRE.FindString(en); m != "" {
		t.Errorf("english fact block contains Chinese (%q) — untranslated copy in the catalog", m)
	}
	if hanRE.FindString(zh) == "" {
		t.Error("chinese fact block renders no Chinese at all")
	}
}

// The AEO ticket is a copy ticket: no FAQPage, no new @type. Whatever the
// template emitted before stays exactly what it emits now.
func TestLandingFactBlockAddsNoSchemaType(t *testing.T) {
	for _, lang := range []string{webi18n.ZhCN, webi18n.En} {
		html := renderLandingLang(t, lang)
		seen := map[string]bool{}
		for _, m := range jsonldTypeRE.FindAllStringSubmatch(html, -1) {
			seen[m[1]] = true
		}
		got := make([]string, 0, len(seen))
		for k := range seen {
			got = append(got, k)
		}
		sort.Strings(got)
		want := append([]string(nil), landingJSONLDTypesBefore...)
		sort.Strings(want)
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("%s landing template now emits @type %v, baseline is %v — the fact block is prose only",
				lang, got, want)
		}
		for _, banned := range []string{"FAQPage", "application/ld+json", "Question\"", "Answer\""} {
			if strings.Contains(html, banned) {
				t.Errorf("%s landing contains %q: no structured data was asked for", lang, banned)
			}
		}
	}
}

// The block is capped at five pairs by the ticket, and every pair must be a
// distinct question with distinct answer drawn from the landing namespace.
func TestLandingFactSetIsWellFormed(t *testing.T) {
	if len(factKeys) != wantFacts {
		t.Errorf("fact block declares %d pairs; the ticket caps it at %d", len(factKeys), wantFacts)
	}
	if len(landingFacts(webi18n.En)) != wantFacts {
		t.Errorf("landingFacts resolved %d pairs, want %d", len(landingFacts(webi18n.En)), wantFacts)
	}
	seen := map[string]bool{}
	for i, k := range factKeys {
		if !strings.HasPrefix(k.qKey, "landing.") || !strings.HasPrefix(k.aKey, "landing.") {
			t.Errorf("fact %d uses %q/%q outside the closed landing namespace", i, k.qKey, k.aKey)
		}
		for _, key := range []string{k.qKey, k.aKey} {
			if seen[key] {
				t.Errorf("catalog key %q is used by two facts", key)
			}
			seen[key] = true
			for _, lang := range []string{webi18n.ZhCN, webi18n.En} {
				v := webi18n.T(lang, key)
				if v == key || v == "" {
					t.Errorf("%s has no copy for %q (rendered %q)", lang, key, v)
				}
			}
		}
		if webi18n.T(webi18n.ZhCN, k.qKey) == webi18n.T(webi18n.ZhCN, k.aKey) {
			t.Errorf("fact %d question and answer are the same string", i)
		}
	}
}
