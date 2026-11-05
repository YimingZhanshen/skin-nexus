// Package webi18n holds the wording of the server-rendered pages (the texture
// detail page, the landing page, the device page) and resolves which language
// a request should get.
//
// Why a package of its own instead of internal/i18n: that one is a leaf by
// design — codes only, no data, no lookup, and a test that reads its source
// with go/ast. Putting a message table there would break the property its own
// doc claims. The two catalogs live here, one file per language, keyed by
// dotted names.
//
// The key namespace deliberately stays out of `errors.*`: the locale gate
// (scripts/check-locales.mjs) fails any `errors.*` key that is not a backend
// error code, and these are page copy, not errors.
package webi18n

import (
	"embed"
	"encoding/json"
	"html/template"
	"net/http"
	"sort"
	"strconv"
	"strings"
)

//go:embed catalogs/en.json catalogs/zh-CN.json
var catalogFS embed.FS

// The only two languages the SSR pages ship. Values match the SPA locale codes
// (web/src/i18n.js) so one `skin_lang` cookie serves both sides.
const (
	ZhCN = "zh-CN"
	En   = "en"
)

// CookieName is the display preference the SPA writes and SSR pages read. Not
// HttpOnly on purpose: the SPA must be able to set it.
const CookieName = "skin_lang"

var catalogs = loadCatalogs()

func loadCatalogs() map[string]map[string]string {
	out := map[string]map[string]string{}
	for _, lang := range []string{ZhCN, En} {
		raw, err := catalogFS.ReadFile("catalogs/" + lang + ".json")
		if err != nil {
			panic("webi18n: read " + lang + ": " + err.Error())
		}
		m := map[string]string{}
		if err := json.Unmarshal(raw, &m); err != nil {
			panic("webi18n: parse " + lang + ": " + err.Error())
		}
		out[lang] = m
	}
	// Parity is checked by scripts/check-locales.mjs as well; failing at boot
	// instead of rendering a bare key on a live page is the cheaper alarm.
	for k := range out[En] {
		if _, ok := out[ZhCN][k]; !ok {
			panic("webi18n: en key " + k + " has no zh-CN entry")
		}
	}
	return out
}

// Keys lists every catalog key (the gate and tests use it).
func Keys() []string {
	var out []string
	for k := range catalogs[ZhCN] {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// T resolves key in lang. Unknown language or key falls back to zh-CN, and a
// key missing there comes back as itself so the gap is visible in the page.
func T(lang, key string) string {
	if m, ok := catalogs[lang]; ok {
		if v, ok := m[key]; ok {
			return v
		}
	}
	if v, ok := catalogs[ZhCN][key]; ok {
		return v
	}
	return key
}

// F is T with `{name}` placeholders filled. Pairs are positional:
// F(lang, "detail.imgAlt", "name", n, "kind", k). A placeholder with no pair is
// left alone, which is what makes a typo show up instead of silently erasing it.
func F(lang, key string, kv ...string) string {
	s := T(lang, key)
	for i := 0; i+1 < len(kv); i += 2 {
		s = strings.ReplaceAll(s, "{"+kv[i]+"}", kv[i+1])
	}
	return s
}

// viewerKeys are the strings the progressive-enhancement module needs at
// runtime. They ship in the injected payload instead of being hardcoded in
// web/viewer-src.js, so one catalog stays the source for both languages.
var viewerKeys = []string{
	"viewer.likeFailed", "viewer.network", "viewer.reportDone",
	"viewer.reportDuplicate", "viewer.reportFailed",
	"viewer.sourceUrlRequired", "viewer.saveFailed",
}

// ViewerPayload is the inline `window.__SKIN_I18N__` for one language.
//
// It is template.JS, not template.HTML: inside a <script> element
// html/template treats any other type as a value to quote and escape, which
// would hand the page `window.__SKIN_I18N__="{\"lang\"...}"` — a string where
// the module expects an object. json.Marshal escapes <, > and &, so a catalog
// value cannot terminate the script element.
func ViewerPayload(lang string) template.JS {
	m := map[string]string{}
	for _, k := range viewerKeys {
		m[strings.TrimPrefix(k, "viewer.")] = T(lang, k)
	}
	b, err := json.Marshal(map[string]any{"lang": normalize(lang), "strings": m})
	if err != nil {
		return ""
	}
	return template.JS(b) //nolint:gosec // marshaled JSON, HTML-escaped above
}

// normalize maps anything unknown to zh-CN — the same default a missing
// preference gets.
func normalize(lang string) string {
	if lang == En {
		return En
	}
	return ZhCN
}

// ExplicitLang answers the language the URL itself names via `?hl=`, or "" when
// the URL says nothing (or names a language we do not ship).
//
// This exists because a locale that lives only in a cookie or an Accept-Language
// header is not a page a crawler can be pointed at: Google's rendering service
// clears cookies and storage during page load, and hreflang alternates have to
// resolve to distinct URLs. `?hl=` is the addressable form, so the emitter uses
// it for the self-canonical and the two alternates.
func ExplicitLang(r *http.Request) string {
	return matchLang(r.URL.Query().Get("hl"))
}

// matchLang folds a language tag to one of the two we ship, or "".
func matchLang(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case strings.ToLower(En):
		return En
	case strings.ToLower(ZhCN):
		return ZhCN
	}
	return ""
}

// ResolveLang picks the language for a request: what the URL says first, then the
// SPA-written preference, then Accept-Language, then zh-CN (the site's origin
// language). A value naming a language we do not ship is treated as no preference
// at all, so a stale cookie or a hand-written `?hl=` cannot pin the wrong language
// on a reader.
func ResolveLang(r *http.Request) string {
	if l := ExplicitLang(r); l != "" {
		return l
	}
	if c, err := r.Cookie(CookieName); err == nil {
		// Compared folded: the constants carry the case the SPA writes
		// ("zh-CN"), a hand-edited cookie need not respect it.
		if l := matchLang(c.Value); l != "" {
			return l
		}
	}
	if l := parseAcceptLanguage(r.Header.Get("Accept-Language")); l != "" {
		return l
	}
	return ZhCN
}

// parseAcceptLanguage walks the quality-ordered tags and answers for the first
// one naming a language we ship. Tags are matched by prefix, so en-US and
// zh-Hans-CN both land where the reader expects.
func parseAcceptLanguage(header string) string {
	if header == "" {
		return ""
	}
	type tag struct {
		q   float64
		iso string
	}
	var tags []tag
	for _, part := range strings.Split(header, ",") {
		item := strings.TrimSpace(part)
		if item == "" {
			continue
		}
		q := 1.0
		if i := strings.Index(item, ";"); i >= 0 {
			params := item[i+1:]
			item = strings.TrimSpace(item[:i])
			for _, p := range strings.Split(params, ";") {
				kv := strings.SplitN(strings.TrimSpace(p), "=", 2)
				if len(kv) == 2 && strings.EqualFold(kv[0], "q") {
					if f := strings.Replace(kv[1], " ", "", -1); f != "" {
						if parsed, ok := scanQ(f); ok {
							q = parsed
						}
					}
				}
			}
		}
		iso := strings.ToLower(item)
		switch {
		case strings.HasPrefix(iso, "en"):
			iso = En
		case strings.HasPrefix(iso, "zh"):
			iso = ZhCN
		default:
			continue
		}
		tags = append(tags, tag{q: q, iso: iso})
	}
	sort.SliceStable(tags, func(i, j int) bool { return tags[i].q > tags[j].q })
	if len(tags) == 0 {
		return ""
	}
	return tags[0].iso
}

func scanQ(s string) (float64, bool) {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}
