package main

// The skin library was the one public page a crawler could not read: it is a
// SPA route, so the served HTML was a title, an empty #app and a spinner. Two
// consequences, both from the engines' own documentation — Bing's guidelines say
// critical content must not live behind client-side rendering, and AI crawlers
// (GPTBot, ClaudeBot, PerplexityBot) do not execute JavaScript at all, so to
// them the library did not exist. It also meant the page that players actually
// search for ("minecraft 皮肤") carried no crawlable text and handed out no
// internal links: every detail page had to be found through the sitemap alone.
//
// So the first page is rendered on the server into the shell's #app, and the SPA
// still takes over on boot: search, filters and infinite interactions stay
// client-side, the crawlable list is server-side. Vue replaces #app's contents
// when it mounts, which is why the prerendered block lives between the two
// markers rather than outside them.

import (
	"context"
	"html/template"
	"net/http"
	"strconv"
	"strings"

	"skin-nexus/server/internal/seo"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/webi18n"
)

// ssrSkinlibPerPage is the server-rendered slice. Deliberately smaller than the
// SPA page size: this markup ships on every crawl of the list, and pagination
// links are what a crawler walks, not an endless grid.
const ssrSkinlibPerPage = 24

type ssrCard struct {
	TID   int64
	Name  string
	Href  string
	Img   string
	Alt   string
	Who   string
	Sort  string
	Type  string
	Query string
}

type ssrListData struct {
	Lang     string
	Title    string
	Count    string
	Cards    []ssrCard
	Empty    bool
	Prev     string
	Next     string
	PrevName string
	NextName string
	Style    template.CSS
	// LangSwitch: 与分页同排但语义不同的语言对照链接（D7/S2）。
	LangSwitch template.HTML
}

// 爬虫可见的皮肤库列表片段。它渲染在宿主文档里，令牌已由应用样式/theme-base 定义，
// 所以这里只消费 --line / --r-* / --fs-* / --muted，不再自带一套灰边与圆角（同
// theme-base.css 的「SSR 里禁止再复制 :root 值」）。次级文字改用 --muted 而不是
// opacity .66/.72 —— 缩小透明度会把小字号的对比度一起拉下去。
const ssrListStyle = `.ssr-list{max-width:1040px;margin:0 auto;padding:24px 20px 40px;color-scheme:light dark}
.ssr-list h1{margin:0 0 8px;font-size:var(--fs-xl)}
.ssr-list .ssr-meta{margin:0 0 16px;color:var(--muted);font-size:var(--fs-s)}
.ssr-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(140px,1fr));gap:16px;list-style:none;margin:0;padding:0}
.ssr-grid a{display:block;text-decoration:none;color:inherit;border:1px solid var(--line);border-radius:var(--r-l);padding:8px}
.ssr-grid img{display:block;width:100%;height:auto;aspect-ratio:1/1;object-fit:cover;border-radius:var(--r-s);image-rendering:pixelated}
.ssr-grid .ssr-n{display:block;margin-top:4px;font-size:var(--fs-s);overflow:hidden;text-overflow:ellipsis;white-space:nowrap}
.ssr-grid .ssr-w{display:block;font-size:var(--fs-xs);color:var(--muted)}
.ssr-pager{display:flex;gap:16px;margin-top:24px;font-size:var(--fs-s)}
.langsw{display:flex;gap:8px;align-items:center;font-size:var(--fs-s)}
.langsw a{color:var(--muted);text-decoration:none;padding:2px 6px;border-radius:var(--r-s)}
.langsw a:hover{color:var(--text);text-decoration:underline}
.langsw a[aria-current="true"]{color:var(--text);font-weight:600;text-decoration:none}
@media (prefers-reduced-motion:reduce){.ssr-grid img{transition:none}}`

var ssrListTmpl = template.Must(template.New("ssr-skinlib").Funcs(tmplFuncs).Parse(`
<style>{{.Style}}</style>
<section class="ssr-list">
  <h1>{{.Title}}</h1>
  <p class="ssr-meta">{{.Count}}</p>
  {{.LangSwitch}}
  {{if .Empty}}<p class="ssr-meta">{{t .Lang "lib.empty"}}</p>{{else}}
  <ul class="ssr-grid">
    {{range .Cards}}
    <li><a href="{{.Href}}"><img src="{{.Img}}" alt="{{.Alt}}" width="128" height="128" loading="lazy" /><span class="ssr-n">{{.Name}}</span><span class="ssr-w">{{.Who}}</span></a></li>
    {{end}}
  </ul>
  {{end}}
  <nav class="ssr-pager">
    {{if .Prev}}<a rel="prev" href="{{.Prev}}">{{.PrevName}}</a>{{end}}
    {{if .Next}}<a rel="next" href="{{.Next}}">{{.NextName}}</a>{{end}}
  </nav>
</section>`))

// skinlibQuery reads the server-side slice of the library request. It accepts
// exactly the filters the API already answers (sort / type / q) so a crawler
// following `?q=` from the site's own SearchAction lands on real results.
func skinlibQuery(r *http.Request) (page int, sort, typ, q string) {
	page = 1
	if n, err := strconv.Atoi(r.URL.Query().Get("page")); err == nil && n > 0 {
		page = n
	}
	switch r.URL.Query().Get("sort") {
	case "likes":
		sort = "likes"
	default:
		sort = "time"
	}
	switch r.URL.Query().Get("type") {
	case "skin", "cape":
		typ = r.URL.Query().Get("type")
	}
	q = strings.TrimSpace(r.URL.Query().Get("q"))
	return page, sort, typ, q
}

// skinlibHref keeps the locale and the filters on every pagination link: a link
// that drops `hl` sends the crawler to the negotiated variant instead of the
// declared one, which is precisely what hreflang says is the same page.
func skinlibHref(base string, page int, sort, typ, q, lang string) string {
	v := map[string][]string{"page": {strconv.Itoa(page)}}
	if sort != "" && sort != "time" {
		v["sort"] = []string{sort}
	}
	if typ != "" {
		v["type"] = []string{typ}
	}
	if q != "" {
		v["q"] = []string{q}
	}
	if lang != "" {
		v["hl"] = []string{lang}
	}
	u := base + "?" + encodeQuery(v)
	return u
}

func encodeQuery(v map[string][]string) string {
	var parts []string
	for _, k := range []string{"page", "sort", "type", "q", "hl"} {
		if vals := v[k]; len(vals) > 0 {
			parts = append(parts, k+"="+urlQueryEscape(vals[0]))
		}
	}
	return strings.Join(parts, "&")
}

func urlQueryEscape(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == ' ' {
			b.WriteByte('+')
			continue
		}
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
			c == '-' || c == '_' || c == '.' || c == '~' {
			b.WriteByte(c)
			continue
		}
		const hex = "0123456789abcdef"
		b.WriteByte('%')
		b.WriteByte(hex[c>>4])
		b.WriteByte(hex[c&0x0f])
	}
	return b.String()
}

// skinlibLister is the slice of storage this prerender needs. Declared here so
// the shell handler does not take the whole store surface as a dependency.
type skinlibLister interface {
	SkinlibList(ctx context.Context, opt store.SkinlibOpts) ([]store.SkinlibRow, int, error)
}

// prerenderSkinlib renders the library's first page for a request. ok=false
// means "leave the shell alone" — a store failure must not turn the list into a
// 500, the SPA can still answer once it boots.
func prerenderSkinlib(ctx context.Context, r *http.Request, st skinlibLister, siteName string) (string, seo.PageMeta, bool) {
	if st == nil {
		return "", seo.PageMeta{}, false
	}
	lang := webi18n.ResolveLang(r)
	page, sort, typ, q := skinlibQuery(r)
	rows, total, err := st.SkinlibList(ctx, store.SkinlibOpts{
		PublicOnly: true, Page: page, PerPage: ssrSkinlibPerPage, Sort: sort, Type: typ, Keyword: q,
	})
	if err != nil {
		return "", seo.PageMeta{}, false
	}
	cards := make([]ssrCard, 0, len(rows))
	for _, row := range rows {
		kind := kindLabel(lang, row.Type)
		alt := row.Name
		if kind != "" {
			alt = row.Name + " — " + kind
		}
		cards = append(cards, ssrCard{
			TID: row.TID, Name: row.Name, Alt: alt,
			Href: "/skinlib/" + strconv.FormatInt(row.TID, 10),
			Img:  "/preview/" + strconv.FormatInt(row.TID, 10),
			Who:  webi18n.F(lang, "lib.by", "name", row.Nickname),
		})
	}
	title := webi18n.T(lang, "lib.title")
	if page > 1 {
		title = title + " " + webi18n.F(lang, "lib.page", "n", strconv.Itoa(page))
	}
	data := ssrListData{
		Lang: lang, Title: title, Style: template.CSS(ssrListStyle),
		Count: webi18n.F(lang, "lib.count", "n", strconv.Itoa(total)),
		Cards: cards, Empty: len(cards) == 0,
		PrevName: webi18n.T(lang, "lib.prev"), NextName: webi18n.T(lang, "lib.next"),
		// The switch is locale, the pager below is pagination: same row shape,
		// different meaning, and neither may silently replace the other.
		LangSwitch: langSwitchFor(r, lang),
	}
	if page > 1 {
		data.Prev = skinlibHref("/skinlib", page-1, sort, typ, q, lang)
	}
	if page*ssrSkinlibPerPage < total {
		data.Next = skinlibHref("/skinlib", page+1, sort, typ, q, lang)
	}
	var sb strings.Builder
	if err := ssrListTmpl.Execute(&sb, data); err != nil {
		return "", seo.PageMeta{}, false
	}
	pm := seo.PageMeta{
		TitleSuffix: title,
		Description: webi18n.F(lang, "lib.description",
			"site", siteName, "n", strconv.Itoa(total)),
		Path: "/skinlib", Lang: lang,
	}
	return sb.String(), pm, true
}

// skinlibAppMarker pairs live in web/index.html inside #app. The server swaps
// what is between them; the boot spinner stays in place for every other route,
// because Vue replacing #app on mount makes the prerendered block the first
// paint rather than a second list.
const (
	skinlibAppStart = "<!--skin-app-start-->"
	skinlibAppEnd   = "<!--skin-app-end-->"
)

func injectAppContent(html, content string) string {
	i := strings.Index(html, skinlibAppStart)
	if i < 0 {
		return html
	}
	rest := html[i+len(skinlibAppStart):]
	j := strings.Index(rest, skinlibAppEnd)
	if j < 0 {
		return html
	}
	return html[:i] + skinlibAppStart + content + rest[j:]
}
