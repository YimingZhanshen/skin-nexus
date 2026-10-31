// SkinNexus · Minecraft skin hosting platform.
// Copyright (C) 2026 SkinNexus contributors. Licensed under AGPL-3.0.
//
// Package appearance assembles the HTML injection layer: active theme pack
// (<!--skin-theme-->) and admin-supplied custom CSS/JS (<!--skin-custom-head-->
// / <!--skin-custom-body-->), with SEO handled by internal/seo.
//
// Injection order matches the blessing convention: theme CSS first, then
// custom CSS (later wins), then custom JS. Custom CSS/JS are trusted admin
// input — rendered verbatim like blessing does, with an explicit risk warning
// in the admin panel. Option values are cached for 60s.
package appearance

import (
	"context"
	"encoding/json"
	"fmt"
	stdhtml "html"
	"html/template"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"skin-nexus/server/internal/theme"
)

const (
	maxFooterLinks     = 8
	maxFooterTextRune  = 1000
	maxFooterLabelRune = 40
	maxFooterURLBytes  = 2048
)

// OptionStore is the persistence port (satisfied by *store.Mysql).
type OptionStore interface {
	GetOption(ctx context.Context, k string) (string, bool, error)
	SetOption(ctx context.Context, k, v string) error
}

// ValidationError marks admin input that should map to HTTP 400 rather than
// an internal error.
type ValidationError struct{ Message string }

func (e *ValidationError) Error() string { return e.Message }

func invalidf(format string, args ...any) error {
	return &ValidationError{Message: fmt.Sprintf(format, args...)}
}

// FooterLink is one admin-configured footer link (friend site, filing
// record, etc.). URLs are restricted to absolute http(s).
type FooterLink struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

// Footer is the public footer data rendered by SSR pages and /site-config.
type Footer struct {
	Text     string       `json:"text"`
	Links    []FooterLink `json:"links"`
	SiteName string       `json:"siteName"`
	Year     int          `json:"year"`
}

// Settings is the admin-editable appearance configuration.
type Settings struct {
	ActiveTheme string       `json:"active_theme"`
	CustomCSS   string       `json:"custom_css"`
	CustomJS    string       `json:"custom_js"`
	CustomHead  string       `json:"custom_head_html"`
	FooterText  string       `json:"footer_text"`
	FooterLinks []FooterLink `json:"footer_links"`
}

// Engine renders the injection blocks.
type Engine struct {
	opts   OptionStore
	themes *theme.Registry

	mu       sync.Mutex
	cached   Settings
	cachedAt time.Time
}

func NewEngine(opts OptionStore, themes *theme.Registry) *Engine {
	return &Engine{opts: opts, themes: themes}
}

// Settings returns the current appearance config (60s cache).
func (e *Engine) Settings(ctx context.Context) Settings {
	e.mu.Lock()
	if !e.cachedAt.IsZero() && time.Since(e.cachedAt) < time.Minute {
		s := e.cached
		e.mu.Unlock()
		return s
	}
	e.mu.Unlock()

	var s Settings
	get := func(k string) string {
		if v, ok, err := e.opts.GetOption(ctx, k); err == nil && ok {
			return v
		}
		return ""
	}
	s.ActiveTheme = get("active_theme")
	s.CustomCSS = get("custom_css")
	s.CustomJS = get("custom_js")
	s.CustomHead = get("custom_head_html")
	s.FooterText = get("footer_text")
	if raw := get("footer_links"); raw != "" {
		var links []FooterLink
		if err := json.Unmarshal([]byte(raw), &links); err == nil {
			s.FooterLinks = sanitizeFooterLinks(links)
		}
	}
	if s.FooterLinks == nil {
		s.FooterLinks = []FooterLink{}
	}

	e.mu.Lock()
	e.cached, e.cachedAt = s, time.Now()
	e.mu.Unlock()
	return s
}

// Save persists the appearance config and busts the cache. An invalid or
// missing theme id clears to "no theme" instead of erroring (the admin panel
// shows the installed list anyway).
func (e *Engine) Save(ctx context.Context, s Settings) error {
	if s.ActiveTheme != "" {
		if _, err := e.themes.Get(s.ActiveTheme); err != nil {
			s.ActiveTheme = ""
		}
	}
	s.FooterText = strings.TrimSpace(s.FooterText)
	if utf8.RuneCountInString(s.FooterText) > maxFooterTextRune {
		return invalidf("页脚文字最多 %d 字", maxFooterTextRune)
	}
	if len(s.FooterLinks) > maxFooterLinks {
		return invalidf("页脚链接最多 %d 条", maxFooterLinks)
	}
	links := make([]FooterLink, 0, len(s.FooterLinks))
	for i, l := range s.FooterLinks {
		l.Label = strings.TrimSpace(l.Label)
		l.URL = strings.TrimSpace(l.URL)
		if l.Label == "" && l.URL == "" {
			continue // an untouched "add link" row must not block saving
		}
		if l.Label == "" || l.URL == "" {
			return invalidf("页脚链接 %d：名称和地址都不能为空", i+1)
		}
		if utf8.RuneCountInString(l.Label) > maxFooterLabelRune {
			return invalidf("页脚链接 %d：名称最多 %d 字", i+1, maxFooterLabelRune)
		}
		if !validFooterURL(l.URL) {
			return invalidf("页脚链接 %d：地址必须是 http(s) 绝对地址", i+1)
		}
		links = append(links, l)
	}
	linksJSON, err := json.Marshal(links)
	if err != nil {
		return err
	}
	pairs := map[string]string{
		"active_theme":     s.ActiveTheme,
		"custom_css":       s.CustomCSS,
		"custom_js":        s.CustomJS,
		"custom_head_html": s.CustomHead,
		"footer_text":      s.FooterText,
		"footer_links":     string(linksJSON),
	}
	for k, v := range pairs {
		if err := e.opts.SetOption(ctx, k, v); err != nil {
			return err
		}
	}
	e.mu.Lock()
	e.cachedAt = time.Time{}
	e.mu.Unlock()
	return nil
}

// FooterData returns the public footer with the site placeholders resolved.
func (e *Engine) FooterData(ctx context.Context, siteName, siteURL string, year int) Footer {
	s := e.Settings(ctx)
	text := strings.NewReplacer(
		"{year}", strconv.Itoa(year),
		"{site_name}", siteName,
		"{site_url}", strings.TrimRight(siteURL, "/"),
	).Replace(s.FooterText)
	links := append([]FooterLink{}, s.FooterLinks...)
	return Footer{
		Text:     text,
		Links:    links,
		SiteName: siteName,
		Year:     year,
	}
}

// HTML renders the shared footer body. Custom text is escaped and keeps
// line breaks; links were validated on save/load, but are escaped again here
// because option rows can be edited directly in the database.
func (f Footer) HTML() template.HTML {
	var b strings.Builder
	if len(f.Links) > 0 {
		b.WriteString(`<nav class="site-footer-links" aria-label="footer links">`)
		for _, l := range f.Links {
			if !validFooterURL(l.URL) {
				continue
			}
			b.WriteString(`<a href="`)
			b.WriteString(stdhtml.EscapeString(l.URL))
			b.WriteString(`" target="_blank" rel="noopener noreferrer">`)
			b.WriteString(stdhtml.EscapeString(l.Label))
			b.WriteString(`</a>`)
		}
		b.WriteString(`</nav>`)
	}
	if f.Text != "" {
		text := stdhtml.EscapeString(strings.ReplaceAll(f.Text, "\r\n", "\n"))
		text = strings.ReplaceAll(text, "\n", "<br>")
		b.WriteString(`<p class="site-footer-text">` + text + `</p>`)
	}
	// Attribution is intentionally not configurable: AGPL §5 requires the
	// copyright notice to stay visible, and hiding it is not a supported knob.
	b.WriteString(`<!-- SkinNexus · Copyright (C) 2026 SkinNexus contributors · AGPL-3.0 -->`)
	b.WriteString(`<p class="site-footer-credit">Powered by <a href="https://github.com/YimingZhanshen/skin-nexus" target="_blank" rel="noopener noreferrer">SkinNexus</a>`)
	if f.SiteName != "" {
		b.WriteString(` · © `)
		b.WriteString(strconv.Itoa(f.Year))
		b.WriteString(` `)
		b.WriteString(stdhtml.EscapeString(f.SiteName))
	}
	b.WriteString(`</p>`)
	return template.HTML(b.String())
}

func validFooterURL(raw string) bool {
	if raw == "" || len(raw) > maxFooterURLBytes {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" {
		return false
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

func sanitizeFooterLinks(in []FooterLink) []FooterLink {
	out := make([]FooterLink, 0, len(in))
	for _, l := range in {
		l.Label = strings.TrimSpace(l.Label)
		l.URL = strings.TrimSpace(l.URL)
		if l.Label == "" || !validFooterURL(l.URL) {
			continue
		}
		if utf8.RuneCountInString(l.Label) > maxFooterLabelRune {
			l.Label = string([]rune(l.Label)[:maxFooterLabelRune])
		}
		out = append(out, l)
		if len(out) == maxFooterLinks {
			break
		}
	}
	return out
}

// Render fills the injection markers in the SPA index.html.
//
// The theme block and custom head block are emitted right before </head> so
// they land AFTER the Vite-built stylesheet link: cascade order decides ties,
// and injecting before the app CSS made theme :root overrides lose against the
// app's own :root definitions (2026-10-25 fix). Theme CSS comes first, custom
// CSS second (custom wins). The <!--skin-theme-->/<!--skin-custom-head-->
// markers are kept in templates as anchors and are removed on render.
func (e *Engine) Render(ctx context.Context, indexHTML string) string {
	s := e.Settings(ctx)

	// theme block
	var themeHTML string
	if s.ActiveTheme != "" {
		if t, err := e.themes.Get(s.ActiveTheme); err == nil {
			themeHTML = t.HeadHTML()
		}
	}

	// custom head: <style> + raw head snippets
	var head strings.Builder
	head.WriteString(themeHTML)
	if s.CustomCSS != "" {
		head.WriteString(`<style id="skin-custom-css">` + "\n" + s.CustomCSS + "\n</style>\n")
	}
	if s.CustomHead != "" {
		head.WriteString(s.CustomHead + "\n")
	}
	indexHTML = strings.ReplaceAll(indexHTML, "<!--skin-theme-->", "")
	indexHTML = strings.ReplaceAll(indexHTML, "<!--skin-custom-head-->", "")
	if i := strings.LastIndex(indexHTML, "</head>"); i >= 0 {
		indexHTML = indexHTML[:i] + head.String() + indexHTML[i:]
	}

	// custom body: JS before </body>
	var body strings.Builder
	if s.CustomJS != "" {
		body.WriteString(`<script id="skin-custom-js">` + "\n" + s.CustomJS + "\n</script>\n")
	}
	return strings.Replace(indexHTML, "<!--skin-custom-body-->", body.String(), 1)
}
