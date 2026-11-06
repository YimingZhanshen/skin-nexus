package main

// SkinNexus · Copyright (C) 2026 SkinNexus contributors · AGPL-3.0

// langswitch.go — the visible half of the SSR bilingual story (D7 / S2).
//
// Since U11 every crawlable page declares its two addressable forms in the
// head (`hreflang` alternates plus x-default), and since S1 the library list
// answers `?q=` on the server — but nothing on the page pointed at either
// language. A reader who wanted English had to already know the query
// parameter exists; a crawler had no internal link to follow, so the two
// variants were reachable only through the sitemap.
//
// The second half is persistence. `?hl=` outranks the cookie inside
// ResolveLang, but only the SPA ever wrote that cookie
// (web/src/i18n.js:17-20), so switching language on a server-rendered page and
// then walking into the app snapped back to whatever Accept-Language said.
// stickyLang closes that gap, and deliberately only for an explicit `?hl=`:
// stamping a cookie onto every negotiated hit would make cached HTML
// per-reader for no behavioral gain.

import (
	"html/template"
	"net/http"
	"net/url"
	"strings"

	"skin-nexus/server/internal/webi18n"
)

// langHrefFor is this very page in one language, in the addressable form the
// hreflang block already advertises: the same path, the same filters, and an
// `hl` that is replaced rather than appended. Two `hl=` values in one href
// would contradict the alternates it mirrors, and dropping a filter would link
// to a different listing while claiming to be the same page in another
// language.
func langHrefFor(r *http.Request, lang string) string {
	q, _ := url.ParseQuery(r.URL.RawQuery)
	q.Set("hl", lang)
	return r.URL.Path + "?" + q.Encode()
}

// langswLink is one entry of the switch.
type langswLink struct {
	Lang    string
	Href    string
	Label   string
	Current bool
}

type langswView struct {
	Label string
	Links []langswLink
}

// langswTmpl renders plain anchors on purpose: an AI crawler does not execute
// JavaScript, and Bing asks that important content not live behind client-side
// rendering — a click handler would be invisible to exactly the audience this
// slice exists for.
var langswTmpl = template.Must(template.New("langsw").Parse(
	`<nav class="langsw" aria-label="{{.Label}}">{{range .Links}}<a rel="alternate" hreflang="{{.Lang}}" href="{{.Href}}"{{if .Current}} aria-current="true"{{end}}>{{.Label}}</a>{{end}}</nav>`))

// langLabelKey names the endonym entry for a language. The label of a language
// is written in that language (中文 / English), so both catalogs carry the same
// two values — the same call G7 made for the SPA's <option> list, and the
// reason neither string is a translation.
func langLabelKey(lang string) string {
	if lang == webi18n.En {
		return "ui.langEn"
	}
	return "ui.langZh"
}

// langSwitchFor renders the pair for a request. `lang` is the language the
// page itself is being served in, which is also the one marked current.
func langSwitchFor(r *http.Request, lang string) template.HTML {
	v := langswView{Label: webi18n.T(lang, "ui.langSwitch")}
	for _, l := range []string{webi18n.ZhCN, webi18n.En} {
		v.Links = append(v.Links, langswLink{
			Lang:    l,
			Href:    langHrefFor(r, l),
			Label:   webi18n.T(lang, langLabelKey(l)),
			Current: l == lang,
		})
	}
	var sb strings.Builder
	if err := langswTmpl.Execute(&sb, v); err != nil {
		// A missing switch is a bilingual regression, not a cosmetic one, so
		// say which half broke instead of shipping a silent nav-less page.
		return template.HTML(`<!--langsw error-->`)
	}
	return template.HTML(sb.String())
}

// stickyLang persists an explicit `?hl=` choice into the same cookie the SPA
// writes, so the app and the crawlable pages stop disagreeing about the
// reader's language.
//
// The attributes mirror web/src/i18n.js:17-20 attribute for attribute.
// Notably no HttpOnly: the SPA reads and writes this cookie, and a server-side
// HttpOnly copy would be a second, divergent preference store. SameSite=Lax
// because a launcher opening /device.html is a top-level navigation that must
// still carry the choice, and Secure is decided per request the same way the
// session cookie decides it (internal/webapi/profile.go:157) — behind a
// TLS-terminating proxy that is X-Forwarded-Proto, and a Secure cookie over
// plain http is simply dropped by the browser.
//
// It is a function the HTML render paths call, not a mux middleware: wrapped
// at registration it was, and the first run of langswitch_test.go caught the
// difference — the handler under test, and any future handler that renders
// landing HTML without going through that one route, silently lost
// persistence. Inside the render path, it cannot be forgotten twice.
func persistLang(w http.ResponseWriter, r *http.Request) {
	lang := webi18n.ExplicitLang(r)
	if lang == "" || langCookieSays(r, lang) {
		return
	}
	secure := r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
	http.SetCookie(w, &http.Cookie{
		Name:     webi18n.CookieName,
		Value:    lang,
		Path:     "/",
		MaxAge:   31536000, // a year, matching the localStorage entry it mirrors
		SameSite: http.SameSiteLaxMode,
		Secure:   secure,
	})
}

// langCookieSays keeps a repeat visit from re-setting an identical cookie: the
// value would be the same, and a Set-Cookie on every page view is cache
// noise, not persistence.
func langCookieSays(r *http.Request, lang string) bool {
	c, err := r.Cookie(webi18n.CookieName)
	return err == nil && strings.EqualFold(c.Value, lang)
}
