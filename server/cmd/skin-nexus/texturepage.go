package main

// SkinNexus · Copyright (C) 2026 SkinNexus contributors · AGPL-3.0

// texturepage.go: the public, server-rendered texture detail page at
// /skinlib/{tid} (2026-10-25 拍板: 皮肤详情取代玩家档案页成为搜索引擎的
// 内容页).
//
// Why server-rendered (not the SPA shell): crawlers must see real content —
// name, uploader, likes, the preview image, JSON-LD — in the response body,
// not an empty <div id="app">. The page consumes the same SEO + appearance
// injection layers as the landing page, so themes and custom CSS apply.
// Interactivity is progressive enhancement: a small fixed-name module
// (assets/viewer.js) upgrades the static server-rendered preview <img> into
// an auto-rotating skinview3d canvas and wires the like toggle; without JS
// the page is still a complete, crawlable, downloadable skin page.

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"time"

	"skin-nexus/server/internal/appearance"
	"skin-nexus/server/internal/avatargen"
	"skin-nexus/server/internal/seo"
	"skin-nexus/server/internal/store"
	"skin-nexus/server/internal/texturemd"
	"skin-nexus/server/internal/webi18n"
)

// tmplFuncs resolve catalog copy at render time, so ONE parsed template serves
// every language. `t` takes (lang, key); `f` also takes positional
// "name", value pairs for {placeholders}.
var tmplFuncs = template.FuncMap{
	"t":      webi18n.T,
	"f":      webi18n.F,
	"kind":   kindLabel,
	"viewer": webi18n.ViewerPayload,
}

// texDetailData feeds the detail template.
type texDetailData struct {
	Lang          string // resolved per request: "zh-CN" | "en"
	SiteName      string
	TID           int64
	Name          string
	Type          string // "skin" | "cape"
	Model         string // "default" | "slim"
	Nickname      string
	Likes         int64
	Liked         bool
	Public        bool
	UploadAt      string // "2006-01-02"-style display date, "" when unknown
	TextureURL    string // absolute, for download + JSON-LD
	ViewerURL     string // absolute, background-normalized sheet for skinview3d
	PreviewURL    string // relative preview image (skin: /preview/{tid}; cape: flat)
	PreviewAbsURL string // absolute preview URL (og:image + JSON-LD)
	DetailHTML    template.HTML
	Source        string
	SourceLabel   string
	SourceURL     string
	Summary       string
	DetailRaw     string
	CanEdit       bool
	LoggedIn      bool
	// Unread: 站内通知未读数，SSR 顶栏角标与 SPA 侧栏同一来源（2026-11-02）
	Unread     int
	Year       int
	FooterHTML template.HTML
	ThemeBase  template.CSS

	// LangSwitch: 顶栏里可见的中·EN 对照链接，渲染前由请求算出（D7/S2）。
	LangSwitch template.HTML

	HeadAnchors template.HTML
	BodyAnchor  template.HTML
	PluginSlot  template.HTML
}

var texDetailTmpl = template.Must(template.New("texdetail").Funcs(tmplFuncs).Parse(`<!DOCTYPE html>
<html lang="{{.Lang}}">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
<link rel="icon" type="image/svg+xml" href="/favicon.svg" />
<link rel="icon" type="image/x-icon" href="/favicon.ico" sizes="48x48" />
<link rel="apple-touch-icon" href="/apple-touch-icon.png" />
<link rel="manifest" href="/manifest.webmanifest" />
<meta name="theme-color" content="#101825" />
{{.HeadAnchors}}
<style>
  /* 详情页设计语言 v3「月下仙山」（与落地页同 token 命名空间；令牌单一真源
     theme-base.css 注入，主题/custom CSS 在其后注入仍可整体覆盖，见 theme-spec） */
  {{.ThemeBase}}
  * { box-sizing: border-box; }
  body { margin: 0; min-height: 100vh; display: flex; flex-direction: column;
         font-family: var(--font-sans);
         font-size: var(--fs-m); line-height: 1.7; -webkit-font-smoothing: antialiased; line-break: strict;
         background: var(--ink); color: var(--moon); }
  ::selection { background: var(--sel); }
  a { color: var(--jade); text-decoration: none; }
  a:hover { color: var(--jade-hi); text-decoration: underline; }
  :focus-visible { outline: 2px solid var(--jade); outline-offset: 2px; }
  .skip-link { position: absolute; left: -9999px; top: 8px; z-index: 90;
               background: var(--panel); color: var(--text); border: 1px solid var(--line-strong);
               border-radius: var(--r-m); padding: 8px 16px; }
  .skip-link:focus { left: 12px; }
  .wrap:focus { outline: none; }
  .topbar { position: sticky; top: 0; z-index: 50; flex: none; height: var(--topbar-h, 56px);
            background: color-mix(in srgb, var(--bg) 82%, transparent);
            -webkit-backdrop-filter: blur(12px); backdrop-filter: blur(12px);
            border-bottom: 1px solid var(--line); }
  .topbar-inner { width: 100%; max-width: 940px; margin: 0 auto; padding: 0 24px; height: 100%;
                  display: flex; align-items: center; justify-content: space-between; gap: 16px; }
  .topbar .brand { display: flex; align-items: center; gap: 8px; min-width: 0; color: var(--moon); }
  .topbar .brand:hover { text-decoration: none; }
  .topbar .brand img { width: 26px; height: 26px; display: block; flex: none; }
  .topbar .brand span { font-family: var(--font-serif); font-size: 20px; font-weight: 600; letter-spacing: .06em;
                        white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .topnav { display: flex; align-items: center; gap: 4px; flex: none; }
  .topnav a { padding: 6px 10px; border-radius: var(--r-s); color: var(--jade); font-size: var(--fs-s); }
  .topnav a:hover { background: var(--hover-wash); color: var(--jade-hi); text-decoration: none; }
  .topnav a.primary { background: var(--cinnabar); color: var(--on-cinnabar); font-weight: 600; }
  .topnav a.primary:hover { background: var(--cinnabar-hi); color: var(--on-cinnabar); }
  .langsw { display: flex; gap: 10px; align-items: center; margin-left: auto; font-size: var(--fs-s); }
  .langsw a { color: var(--muted); text-decoration: none; padding: 4px 8px; border-radius: var(--r-s); }
  .langsw a:hover { color: var(--text); text-decoration: underline; }
  .langsw a[aria-current="true"] { color: var(--text); font-weight: 600; text-decoration: none; }
  .unread-badge { display: inline-block; min-width: 16px; margin-left: 6px; padding: 0 5px; border-radius: 9px; background: var(--cinnabar); color: var(--on-cinnabar); font-size: 11px; line-height: 17px; text-align: center; font-weight: 600; }
  .wrap { flex: 1 0 auto; width: 100%; max-width: 940px; margin: 0 auto; padding: 32px 24px 56px; }
  .crumb { font-size: var(--fs-s); color: var(--muted); margin: 0 0 16px; }
  .crumb .sep { color: var(--muted); }
  .card { background: var(--panel); border: 1px solid var(--line);
          border-radius: var(--r-l); padding: 32px;
          box-shadow: var(--shadow); }
  h1 { margin: 0 0 6px; font-family: var(--font-serif); font-size: 30px; font-weight: 600;
       letter-spacing: .03em; overflow-wrap: anywhere; }
  .sub { color: var(--moon-2); font-size: var(--fs-m); margin: 0 0 24px; }
  .viewer { display: flex; justify-content: center; align-items: center;
            min-height: 340px; margin: 0 0 24px; position: relative; }
  .viewer img.static { max-height: 320px; image-rendering: pixelated; }
  .viewer canvas { display: none; }
  .viewer.ready img.static { display: none; }
  .viewer.ready canvas { display: block; }
  .meta { display: grid; grid-template-columns: repeat(auto-fill, minmax(170px, 1fr));
          gap: 12px; margin: 0 0 24px; }
  .meta div { position: relative; background: var(--ink-3); border: 1px solid var(--line);
              border-radius: var(--r-m); padding: 10px 12px; }
  /* 赭石回纹角饰（与落地页统计卡同款小音量） */
  .meta div::before, .meta div::after { content: ""; position: absolute; width: 8px; height: 8px;
              border: 1.5px solid var(--gold); opacity: .55; }
  .meta div::before { top: 5px; left: 5px; border-right: none; border-bottom: none; }
  .meta div::after { bottom: 5px; right: 5px; border-left: none; border-top: none; }
  .meta dt { font-size: var(--fs-xs); color: var(--moon-2); margin: 0 0 2px; }
  .meta dd { margin: 0; font-size: var(--fs-s); font-weight: 600; overflow-wrap: anywhere;
             font-variant-numeric: tabular-nums; }
  .actions { display: flex; gap: 8px; flex-wrap: wrap; align-items: center; }
  .actions a, .actions button { display: inline-block; padding: 9px 20px; border-radius: var(--r-s);
        font-size: var(--fs-s); font-weight: 600; text-decoration: none; cursor: pointer;
        font-family: inherit; transition: background var(--t-fast) var(--ease),
        border-color var(--t-fast) var(--ease), transform var(--t-fast) var(--ease),
        box-shadow var(--t-fast) var(--ease); }
  .actions .primary { background: var(--cinnabar); color: var(--on-cinnabar); border: none; }
  .actions .primary:hover { background: var(--cinnabar-hi); transform: translateY(-1px);
        box-shadow: 0 6px 16px -8px var(--glow-cinnabar); text-decoration: none; }
  .actions .primary:active { transform: translateY(0); }
  .actions .alt { border: 1px solid var(--line-strong); color: var(--moon);
                  background: transparent; }
  .actions .alt:hover { background: var(--ink-3); border-color: var(--gold);
                  color: var(--moon); text-decoration: none; }
  .like-btn.liked { color: var(--danger); border-color: var(--danger); }
  footer.site-footer { position: relative; flex: none; margin-top: 32px; padding: 18px 24px 24px;
           text-align: center; font-size: var(--fs-xs); color: var(--muted); border-top: 1px solid var(--line); }
  footer.site-footer a { color: var(--jade); }
  footer.site-footer a:hover { color: var(--jade-hi); }
  .site-footer-links { display: flex; flex-wrap: wrap; justify-content: center; gap: 6px 18px; margin: 0 0 8px; }
  .site-footer-text { margin: 0 0 6px; white-space: pre-line; }
  .site-footer-credit { margin: 0; }
  .detail-block { margin-top: 18px; padding-top: 14px; border-top: 1px solid var(--border); }
  .detail-block h2 { font-size: var(--fs-m); margin: 0 0 8px; }
  .md.md.md-wide p, .md.md.md-wide li { max-width: none; }
  .source-line { margin: 0 0 8px; color: var(--muted); font-size: var(--fs-s); }
  .source-line a { color: var(--jade); }
  .report-dialog { border: 1px solid var(--border); border-radius: var(--r-m); background: var(--panel);
    color: var(--text); max-width: 420px; width: calc(100% - 32px); padding: 18px; }
  .report-dialog label { display: block; margin: 10px 0; font-size: var(--fs-s); }
  .report-dialog select, .report-dialog textarea { width: 100%; margin-top: 4px; }
  .report-actions { display: flex; gap: 8px; justify-content: flex-end; margin-top: 12px; }
  .report-error { color: var(--danger-ink, var(--danger, #d9564c)); font-size: var(--fs-s); }
  @media (prefers-reduced-motion: reduce) { * { animation: none !important; transition: none !important; } }
  @media (max-width: 560px) {
    .topbar-inner { padding: 0 14px; }
    .topbar .brand span { display: none; }
    .topnav a { padding: 5px 8px; }
    .wrap { padding: 20px 14px 40px; }
    .card { padding: 20px; }
    h1 { font-size: 23px; }
    .viewer { min-height: 240px; }
  }
</style>
<!--skin-theme-->
<!--skin-custom-head-->
</head>
<body>
  <a class="skip-link" href="#main-content">{{t .Lang "page.skip"}}</a>
  <header class="topbar">
    <div class="topbar-inner">
      <a class="brand" href="/">
        <img src="/favicon.svg" alt="" />
        <span>{{.SiteName}}</span>
      </a>
      <nav class="topnav" aria-label="{{t .Lang "page.topnav"}}">
        <a href="/skinlib">{{t .Lang "page.skinlib"}}</a>
        {{if .LoggedIn}}<a href="/user/dashboard">{{t .Lang "page.userCenter"}}{{if .Unread}}<span class="unread-badge">{{.Unread}}</span>{{end}}</a>{{else}}<a href="/login">{{t .Lang "page.login"}}</a><a class="primary" href="/register">{{t .Lang "page.register"}}</a>{{end}}
      </nav>
      {{.LangSwitch}}
    </div>
  </header>
  <div class="wrap" id="main-content" tabindex="-1">
    <nav class="crumb" aria-label="{{t .Lang "page.breadcrumb"}}"><a href="/">{{t .Lang "page.home"}}</a><span class="sep" aria-hidden="true"> › </span><a href="/skinlib">{{t .Lang "page.skinlib"}}</a><span class="sep" aria-hidden="true"> › </span><span aria-current="page">{{.Name}}</span></nav>
    <article class="card" itemscope itemtype="https://schema.org/CreativeWork">
      <h1 itemprop="name">{{.Name}}</h1>
      <p class="sub">Minecraft {{kind .Lang .Type}} · {{t .Lang "detail.uploadedBy"}} <span itemprop="author">{{.Nickname}}</span></p>
      <div class="viewer" id="tex-viewer">
        <img class="static" src="{{.PreviewURL}}" alt="{{f .Lang "detail.imgAlt" "name" .Name "kind" (kind .Lang .Type)}}" itemprop="image" />
        {{if eq .Type "skin"}}<canvas data-skinview3d data-texture="{{.ViewerURL}}" data-model="{{.Model}}" width="320" height="420"></canvas>{{end}}
      </div>
      <dl class="meta">
        <div><dt>{{t .Lang "detail.metaType"}}</dt><dd>{{if eq .Type "cape"}}{{t .Lang "detail.kindCape"}}{{else if eq .Model "slim"}}{{t .Lang "detail.typeSkinSlim"}}{{else}}{{t .Lang "detail.typeSkinDefault"}}{{end}}</dd></div>
        <div><dt>{{t .Lang "detail.metaId"}}</dt><dd>#{{.TID}}</dd></div>
        <div><dt>{{t .Lang "detail.metaLikes"}}</dt><dd><span id="like-count">{{.Likes}}</span> {{t .Lang "detail.likesUnit"}}</dd></div>
        {{if .UploadAt}}<div><dt>{{t .Lang "detail.metaUploadAt"}}</dt><dd>{{.UploadAt}}</dd></div>{{end}}
        <div><dt>{{t .Lang "detail.metaVisibility"}}</dt><dd>{{if .Public}}{{t .Lang "detail.visibilityPublic"}}{{else}}{{t .Lang "detail.visibilityPrivate"}}{{end}}</dd></div>
      </dl>
      {{if or .DetailHTML .SourceLabel}}
      <section class="detail-block">
        <h2>{{t .Lang "detail.descTitle"}}</h2>
        {{if .SourceLabel}}<p class="source-line">{{t .Lang "detail.sourceLine"}}{{if .SourceURL}}<a href="{{.SourceURL}}" target="_blank" rel="nofollow noopener">{{.SourceLabel}}</a>{{else}}{{.SourceLabel}}{{end}}</p>{{end}}
        {{if .DetailHTML}}<div class="md md-wide">{{.DetailHTML}}</div>{{end}}
      </section>
      {{end}}
      <div class="actions">
        <button class="alt like-btn{{if .Liked}} liked{{end}}" data-like="{{.TID}}" data-liked="{{if .Liked}}1{{else}}0{{end}}">♥ <span>{{t .Lang "detail.likeLabel"}}</span></button>
        <a class="primary" href="{{.TextureURL}}" download>{{t .Lang "detail.download"}}</a>
        {{if .LoggedIn}}<a class="alt" href="/skinlib#wear-{{.TID}}-{{.Type}}">{{t .Lang "detail.wear"}}</a>{{else}}<a class="alt" href="/register">{{t .Lang "detail.registerToUse"}}</a>{{end}}
        {{if .CanEdit}}<button class="alt" id="edit-btn">{{t .Lang "detail.edit"}}</button>{{end}}
        {{if .Public}}<button class="alt report-btn" id="report-btn" data-tid="{{.TID}}">{{t .Lang "detail.report"}}</button>{{end}}
      </div>
    </article>
    {{.PluginSlot}}
  </div>
  {{if .FooterHTML}}<footer class="site-footer">{{.FooterHTML}}</footer>{{end}}
  {{if .CanEdit}}
  <dialog id="edit-dialog" class="report-dialog">
    <form id="edit-form" method="dialog">
      <h2>{{t .Lang "edit.title"}}</h2>
      <label>{{t .Lang "edit.name"}}<input id="edit-name" maxlength="50" value="{{.Name}}" /></label>
      <label>{{t .Lang "edit.source"}}
        <select id="edit-source">
          <option value="original"{{if eq .Source "original"}} selected{{end}}>{{t .Lang "edit.sourceOriginal"}}</option>
          <option value="derivative"{{if eq .Source "derivative"}} selected{{end}}>{{t .Lang "edit.sourceDerivative"}}</option>
          <option value="repost"{{if eq .Source "repost"}} selected{{end}}>{{t .Lang "edit.sourceRepost"}}</option>
        </select>
      </label>
      <label>{{t .Lang "edit.sourceUrl"}}<input id="edit-source-url" type="url" value="{{.SourceURL}}" placeholder="https://" /></label>
      <label>{{t .Lang "edit.detail"}}<textarea id="edit-detail" rows="5" maxlength="4000">{{.DetailRaw}}</textarea></label>
      <p class="report-error" id="edit-error" hidden></p>
      <div class="report-actions">
        <button type="button" class="alt" id="edit-cancel">{{t .Lang "ui.cancel"}}</button>
        <button type="submit">{{t .Lang "ui.save"}}</button>
      </div>
    </form>
  </dialog>
  {{end}}
  <dialog id="report-dialog" class="report-dialog">
    <form id="report-form" method="dialog">
      <h2>{{t .Lang "report.title"}}</h2>
      <label>{{t .Lang "report.reason"}}
        <select id="report-reason">
          <option value="copyright">{{t .Lang "report.reasonCopyright"}}</option>
          <option value="sexual">{{t .Lang "report.reasonSexual"}}</option>
          <option value="violence">{{t .Lang "report.reasonViolence"}}</option>
          <option value="hate">{{t .Lang "report.reasonHate"}}</option>
          <option value="spam">{{t .Lang "report.reasonSpam"}}</option>
          <option value="other">{{t .Lang "report.reasonOther"}}</option>
        </select>
      </label>
      <label>{{t .Lang "report.detail"}}
        <textarea id="report-detail" rows="3" maxlength="500"></textarea>
      </label>
      <p class="report-error" id="report-error" hidden></p>
      <div class="report-actions">
        <button type="button" class="alt" id="report-cancel">{{t .Lang "ui.cancel"}}</button>
        <button type="submit" value="submit">{{t .Lang "report.submit"}}</button>
      </div>
    </form>
  </dialog>
  <script>window.__SKIN_I18N__={{viewer .Lang}};</script>
  <script type="module" src="/assets/viewer.js?v=1"></script>
  {{.BodyAnchor}}
</body>
</html>
`))

// texNotFoundTmpl is the minimal branded 404 for missing/private textures —
// a plain http.NotFound would drop the theme and look like a server fault.
var texNotFoundTmpl = template.Must(template.New("tex404").Funcs(tmplFuncs).Parse(`<!DOCTYPE html>
<html lang="{{.Lang}}">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
<title>{{.Title}}</title>
<meta name="robots" content="noindex, nofollow" />
<link rel="icon" type="image/svg+xml" href="/favicon.svg" />
<style>
  :root { color-scheme: dark; --ink: #16202e; --moon: #e9edf1; --jade: #67b7a0;
          --font-serif: "Noto Serif SC", "Source Han Serif SC", "Songti SC", "SimSun", serif; }
  body { margin: 0; font-family: "Segoe UI", "PingFang SC", "HarmonyOS Sans SC", "Microsoft YaHei", sans-serif;
         background: var(--ink); color: var(--moon); display: grid; place-items: center; min-height: 100vh; }
  .box { text-align: center; }
  .box h1 { font-family: var(--font-serif); font-size: 40px; margin: 0 0 8px; }
  a { color: var(--jade); }
</style>
<!--skin-theme-->
<!--skin-custom-head-->
</head>
<body><div class="box"><h1>404</h1><p>{{.Msg}}</p><p><a href="/">{{t .Lang "notfound.back"}}</a></p></div></body>
</html>
`))

// texJSONLD builds the schema.org block for one public texture. All values
// pass through json.Marshal (which escapes HTML metacharacters) and the
// injection layer additionally neutralizes "</".
func texJSONLD(d texDetailData, canonical string) string {
	obj := map[string]any{
		"@context": "https://schema.org",
		"@type":    "CreativeWork",
		"name":     d.Name,
		"image":    d.PreviewAbsURL,
		"url":      canonical,
		"author":   map[string]any{"@type": "Person", "name": d.Nickname},
		"genre":    "Minecraft texture",
		"interactionStatistic": map[string]any{
			"@type":                "InteractionCounter",
			"interactionType":      "https://schema.org/LikeAction",
			"userInteractionCount": d.Likes,
		},
	}
	if d.UploadAt != "" {
		obj["datePublished"] = d.UploadAt
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return ""
	}
	return string(b)
}

// texturePageHandler serves GET /skinlib/{tid}. Visibility: public textures
// render for everyone; private textures render only for their owner (or an
// admin) and carry noindex; anything else 404s with a branded page.
func texturePageHandler(st *store.Mysql, seoEng *seo.Engine, appearEng *appearance.Engine, siteName, baseURL string, texURL func(ctx context.Context, hash, url, typ string) string, slotHTML func(string, map[string]string) string) http.HandlerFunc {
	base := strings.TrimRight(baseURL, "/")
	return func(w http.ResponseWriter, r *http.Request) {
		persistLang(w, r)
		lang := webi18n.ResolveLang(r)
		tid, err := strconv.ParseInt(r.PathValue("tid"), 10, 64)
		if err != nil || tid <= 0 {
			writeTexNotFound(r.Context(), w, siteName, appearEng, lang, "notfound.title", "notfound.texture")
			return
		}
		// viewer identity: session cookie → owner/admin visibility
		var uid int64
		var admin bool
		if c, err := r.Cookie("skin_session"); err == nil {
			if id, err := st.SessionUser(r.Context(), c.Value); err == nil {
				uid = id
				if perm, err := st.UserPermission(r.Context(), id); err == nil && perm > 0 {
					admin = true
				}
			}
		}
		row, err := st.SkinlibOne(r.Context(), tid, uid, admin)
		if err == store.ErrNotFound {
			writeTexNotFound(r.Context(), w, siteName, appearEng, lang, "notfound.title", "notfound.texture")
			return
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		d := texDetailData{
			Lang:        lang,
			SiteName:    siteName,
			TID:         row.TID,
			Name:        row.Name,
			Type:        row.Type,
			Model:       row.Model,
			Nickname:    row.Nickname,
			Likes:       row.Likes,
			Liked:       row.Liked,
			Public:      row.Public,
			Source:      row.Source,
			SourceURL:   row.SourceURL,
			DetailHTML:  template.HTML(texturemd.Render(row.DetailMD)),
			Summary:     texturemd.Excerpt(row.DetailMD, 160),
			SourceLabel: sourceLabel(lang, row.Source),
			DetailRaw:   row.DetailMD,
			CanEdit:     uid != 0 && (uid == row.UID || admin),
			LoggedIn:    uid != 0,
		}
		if uid != 0 {
			// Same source as the SPA sidebar badge; a count failure just hides the badge.
			if n, err := st.UnreadNotificationCount(r.Context(), uid); err == nil {
				d.Unread = n
			}
		}
		if d.Name == "" {
			d.Name = webi18n.F(lang, "detail.untitled", "tid", strconv.FormatInt(row.TID, 10))
		}
		if row.UploadAt.Valid {
			d.UploadAt = row.UploadAt.Time.UTC().Format("2006-01-02")
		}
		// Same split as the skinlib UI / landing cards: skins get the
		// server-rendered body preview, capes the flat texture (a cape
		// through the body renderer is garbage).
		if row.Type == "skin" {
			d.PreviewURL = "/preview/" + strconv.FormatInt(row.TID, 10) + "?v=" + avatargen.RenderVersion
		} else {
			d.PreviewURL = "/textures/" + row.Hash
		}
		d.TextureURL = st.TextureURL(row.Hash)
		if texURL != nil {
			d.TextureURL = texURL(r.Context(), row.Hash, d.TextureURL, row.Type)
		}
		d.PreviewAbsURL = absURL(base, d.PreviewURL)
		d.ViewerURL = absURL(base, "/texture-render/"+row.Hash+"?v="+avatargen.RenderVersion)
		if slotHTML != nil {
			d.PluginSlot = template.HTML(slotHTML("skinlib.detail.sidebar", map[string]string{
				"tid":  strconv.FormatInt(row.TID, 10),
				"hash": row.Hash,
			}))
		}

		canonical := base + "/skinlib/" + strconv.FormatInt(row.TID, 10)
		pm := seo.PageMeta{
			TitleSuffix: webi18n.F(d.Lang, "seo.titleSuffix", "name", d.Name, "kind", kindLabel(d.Lang, d.Type)),
			Description: texDescription(d),
			Path:        "/skinlib/" + strconv.FormatInt(row.TID, 10),
			OGImage:     d.PreviewAbsURL,
			Lang:        d.Lang,
			Copy:        seoCopyFor(d.Lang),
			JSONLD:      texJSONLD(d, canonical),
		}
		// Private content viewed by its owner must never be indexed.
		pm.NoIndex = !row.Public

		var sb strings.Builder
		d.Year = time.Now().Year()
		d.FooterHTML = appearEng.FooterData(r.Context(), siteName, baseURL, d.Year).HTML()
		if d.ThemeBase == "" {
			d.ThemeBase = themeBaseCSS
		}
		d.HeadAnchors = template.HTML(landingHeadAnchors)
		d.BodyAnchor = template.HTML(landingBodyAnchor)
		d.LangSwitch = langSwitchFor(r, d.Lang)
		if err := texDetailTmpl.Execute(&sb, d); err != nil {
			http.Error(w, "render error", http.StatusInternalServerError)
			return
		}
		html := seoEng.RenderHeadWith(r.Context(), sb.String(), pm)
		html = appearEng.Render(r.Context(), html)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	}
}

// absURL joins the site base with a relative, possibly query-bearing path.
func absURL(base, rel string) string {
	if strings.HasPrefix(rel, "http://") || strings.HasPrefix(rel, "https://") {
		return rel
	}
	return strings.TrimRight(base, "/") + rel
}

// kindLabel names the texture type in the reader's language. The catalog holds
// it because the same word shows up in the title, the subtitle, the alt text
// and the meta description — four places that must not disagree.
func kindLabel(lang, typ string) string {
	if typ == "cape" {
		return webi18n.T(lang, "detail.kindCape")
	}
	return webi18n.T(lang, "detail.kindSkin")
}

func texDescription(d texDetailData) string {
	kind := kindLabel(d.Lang, d.Type)
	model := ""
	if d.Type == "skin" {
		if d.Model == "slim" {
			model = webi18n.T(d.Lang, "seo.modelSlim")
		} else {
			model = webi18n.T(d.Lang, "seo.modelDefault")
		}
	}
	intro := d.Name
	if d.Summary != "" {
		intro = webi18n.F(d.Lang, "seo.introWithSummary", "name", d.Name, "summary", d.Summary)
	}
	return webi18n.F(d.Lang, "seo.description",
		"intro", intro, "site", d.SiteName, "kind", kind, "model", model,
		"who", d.Nickname, "likes", strconv.FormatInt(d.Likes, 10))
}

// writeTexNotFound renders the branded 404 with theme/custom-css injection
// (the anchors above feed appearEng.Render) and a true 404 status. The two keys
// are the window title and the body line: a missing texture and a missing page
// read differently, and the title used to say 材质不存在 even for an unknown URL.
func writeTexNotFound(ctx context.Context, w http.ResponseWriter, siteName string, appearEng *appearance.Engine, lang, titleKey, msgKey string) {
	var sb strings.Builder
	_ = texNotFoundTmpl.Execute(&sb, map[string]string{
		"SiteName": siteName, "Lang": lang,
		"Title": webi18n.F(lang, titleKey, "site", siteName),
		"Msg":   webi18n.T(lang, msgKey),
	})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	html := sb.String()
	if appearEng != nil {
		html = appearEng.Render(ctx, html)
	}
	_, _ = w.Write([]byte(html))
}

// sourceLabel renders the uploader's declared origin in the reader's language,
// or the empty string when the row carries no source at all (older rows and
// imported ones — the section then stays hidden entirely).
func sourceLabel(lang, source string) string {
	switch source {
	case "original":
		return webi18n.T(lang, "edit.sourceOriginal")
	case "derivative":
		return webi18n.T(lang, "edit.sourceDerivative")
	case "repost":
		return webi18n.T(lang, "edit.sourceRepost")
	default:
		return ""
	}
}
