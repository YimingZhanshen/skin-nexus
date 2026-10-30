// Package texturemd renders user-supplied texture descriptions. It is
// deliberately stricter than the site announcement renderer: remote images
// are removed (tracking/NSFW), and only plain formatting plus http(s) links
// survive sanitization.
package texturemd

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/renderer/html"
)

var md = goldmark.New(
	goldmark.WithExtensions(extension.GFM),
	goldmark.WithRendererOptions(html.WithUnsafe()),
)

// policy allows formatting, lists, code, tables and http(s) links only.
// Images/iframes/forms/styles are never allowed, so remote tracking or
// off-site NSFW content cannot ride along with a description.
var policy = func() *bluemonday.Policy {
	p := bluemonday.NewPolicy()
	p.AllowURLSchemes("http", "https")
	p.AllowAttrs("href", "title").OnElements("a")
	p.RequireNoFollowOnLinks(true)
	p.RequireNoReferrerOnLinks(true)
	p.AllowElements(
		"p", "br", "strong", "em", "b", "i", "u", "s", "del", "ins",
		"ul", "ol", "li", "h1", "h2", "h3", "h4", "h5", "h6",
		"blockquote", "code", "pre", "hr", "table", "thead", "tbody",
		"tr", "th", "td",
	)
	return p
}()

// Render converts description Markdown to sanitized HTML. Empty input returns
// empty; render failures degrade to empty rather than echo raw input.
func Render(src string) string {
	if strings.TrimSpace(src) == "" {
		return ""
	}
	var buf bytes.Buffer
	if err := md.Convert([]byte(src), &buf); err != nil {
		return ""
	}
	return policy.Sanitize(buf.String())
}

// Excerpt returns a plain-text summary for meta tags/cards (no HTML).
func Excerpt(src string, n int) string {
	if n <= 0 {
		n = 160
	}
	plain := bluemonday.StrictPolicy().Sanitize(Render(src))
	plain = strings.Join(strings.Fields(plain), " ")
	if utf8.RuneCountInString(plain) <= n {
		return plain
	}
	r := []rune(plain)
	return string(r[:n]) + "…"
}
