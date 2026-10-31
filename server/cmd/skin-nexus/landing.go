package main

// SkinNexus · Copyright (C) 2026 SkinNexus contributors · AGPL-3.0

// landing.go: the public, server-rendered landing page served at "/".
//
// Rationale (2026-10-25 拍板): the SPA login card is fine for returning players
// but a dead end for first-time visitors, crawlers and AI agents. The landing
// page is rendered by Go templates straight into the response body — readable
// without JavaScript — and reuses the SEO + appearance injection layers so the
// theme / custom CSS apply (the page consumes the same design tokens as the
// app, with fallbacks).

import (
	"context"
	"html/template"
	"strings"
	"time"

	"skin-nexus/server/internal/appearance"
	"skin-nexus/server/internal/seo"
)

// landingTexture is one featured library card. URL follows the skinlib UI
// rule (SkinlibPage.vue): skins get the server-rendered body preview, capes
// get the flat texture image (/preview of a cape through the body renderer
// is garbage, hence 404 there).
type landingTexture struct {
	TID  int64
	Name string
	URL  string
}

// landingData feeds the landing template.
type landingData struct {
	SiteName   string
	SiteURL    string
	Intro      string
	Users      int
	Players    int
	Textures   int
	Featured   []landingTexture
	Year       int
	FooterHTML template.HTML
	// LoggedIn: 已登录访客不再展示登录/注册 CTA（2026-10-27 逻辑修复）
	LoggedIn bool
}

const landingIntro = "Minecraft 皮肤站：上传皮肤/披风、管理角色衣柜、逛皮肤库；配合 Yggdrasil 外置登录，启动器与服务器之间直接换装。"

var landingTmpl = template.Must(template.New("landing").Parse(`<!DOCTYPE html>
<html lang="zh-CN">
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
  /* 落地页设计语言「月下仙山」（2026-10-26 用户 brief：中国古风仙侠）。
     自有 token 命名空间：主题包/custom CSS 在其后注入，仍可整体覆盖（theme-spec §注入顺序）。
     国画色：黛青底 / 月白字 / 朱砂只给主按钮 / 赭石纹线；无大红大紫大金。
     签名元素 = N 字牌（保留原入场编舞，重着色）；远山云雾为唯一景。 */
  * { box-sizing: border-box; }
  :root {
    color-scheme: dark;
    --font-serif: "Noto Serif SC", "Source Han Serif SC", "Songti SC", "SimSun", serif;
    --bg: #101825; --panel: #16202e;
    --border: rgba(169, 138, 95, .24); --text: #e9edf1; --muted: #a8b4c3;
    --accent: #b0453a;
    --ink: var(--bg); --ink-2: var(--panel); --ink-3: #1d2a3d;
    --line: var(--border); --line-strong: rgba(169, 138, 95, .45);
    --moon: var(--text); --moon-2: var(--muted);
    --cinnabar: var(--accent); --cinnabar-hi: #c25446; --on-cinnabar: #fdf3ec;
    --jade: #67b7a0; --jade-hi: #7fcbb3; --gold: #c9a06a; --ochre: var(--gold);
    --r-s: 4px; --r-m: 6px; --r-l: 10px;
    --shadow: 0 12px 32px -18px rgba(3, 8, 16, .8);
  }
  body { margin: 0; min-height: 100vh; display: flex; flex-direction: column;
         font-family: "Segoe UI", "Inter", "PingFang SC", "HarmonyOS Sans SC", "Noto Sans CJK SC", "Microsoft YaHei", sans-serif;
         font-size: 15px; line-height: 1.7; -webkit-font-smoothing: antialiased; line-break: strict;
         background: var(--ink); color: var(--moon); }
  /* 远山云雾：全页唯一景（fixed 垫在内容层后，静谧不抢内容） */
  .scene { position: fixed; inset: auto 0 0 0; height: 44vh; min-height: 250px;
           pointer-events: none; z-index: 0; overflow: hidden; }
  .scene-mountains { position: absolute; inset: auto 0 0 0; width: 100%; height: 100%; display: block; }
  .scene-fog { position: absolute; left: -60%; width: 220%; height: 110px;
               background: radial-gradient(50% 100% at 50% 100%, rgba(233, 237, 241, .05), transparent 70%); }
  .fog-a { bottom: 56px; animation: fogdrift 70s linear infinite; }
  .fog-b { bottom: 150px; opacity: .7; animation: fogdrift 110s linear infinite reverse; }
  @keyframes fogdrift { from { transform: translateX(-4%); } to { transform: translateX(4%); } }
  .wrap { position: relative; z-index: 1; flex: 1 0 auto; width: 100%; max-width: 1040px; margin: 0 auto; padding: 32px 24px 56px; }
  /* 首屏 */
  .hero { position: relative; padding: 72px 0 44px; text-align: center; }
  .hero > * { position: relative; }
  .hero h1 { margin: 0 0 10px; font-family: var(--font-serif); font-size: 48px; font-weight: 600;
             letter-spacing: .06em; line-height: 1.25; }
  .hero .tag { color: var(--moon-2); font-size: 16px; margin: 0 0 28px; }
  .cta a { display: inline-block; margin: 0 8px; padding: 11px 28px; border-radius: 4px;
           font-size: 15px; text-decoration: none; font-weight: 600; transition: background .12s, border-color .12s; }
  .cta .primary { background: var(--cinnabar); color: var(--on-cinnabar); }
  .cta .primary:hover { background: var(--cinnabar-hi); }
  .cta .alt { border: 1px solid var(--line); color: var(--moon); }
  .cta .alt:hover { background: var(--ink-2); border-color: var(--ochre); }
  .cta a:focus-visible { outline: 2px solid var(--ochre); outline-offset: 2px; }
  /* 统计卡：赭石回纹角饰（小音量），数字用赭石（朱砂只留给主按钮） */
  .stats { display: flex; gap: 16px; margin: 32px 0; }
  .stats div { position: relative; flex: 1; text-align: center; padding: 22px 12px; border-radius: var(--r-l);
               background: var(--panel); border: 1px solid var(--line); box-shadow: var(--shadow);
               font-variant-numeric: tabular-nums; }
  .stats div::before, .stats div::after { content: ""; position: absolute; width: 10px; height: 10px;
               border: 1.5px solid var(--ochre); opacity: .6; }
  .stats div::before { top: 6px; left: 6px; border-right: none; border-bottom: none; }
  .stats div::after { bottom: 6px; right: 6px; border-left: none; border-top: none; }
  .stats b { display: block; font-size: 30px; color: var(--ochre); font-weight: 600; }
  .stats span { font-size: 14px; color: var(--moon-2); }
  /* 祥云分隔：一枝纹样 repeat-x */
  .divider { height: 26px; margin: 4px 0 0; opacity: .55; background-repeat: repeat-x;
             background-position: center; background-size: auto 26px;
             background-image: url("data:image/svg+xml,%3Csvg xmlns='http://www.w3.org/2000/svg' width='132' height='26' viewBox='0 0 132 26'%3E%3Cpath d='M8 19c0-5 4-8 8-7 1-6 10-8 13-2 5-2 10 1 9 6 4 0 7 1 9 3' fill='none' stroke='%23c9a06a' stroke-width='1.5' stroke-linecap='round'/%3E%3Cpath d='M60 19c7 0 10-6 16-6 7 0 9 6 16 6 6 0 9-4 14-4' fill='none' stroke='%23c9a06a' stroke-width='1.2' opacity='.65'/%3E%3C/svg%3E"); }
  /* 皮肤库入口：次级按钮（与 CTA 描边款同语言，赭石线 + 金边 hover） */
  .more { text-align: center; margin: 20px 0 0; }
  .more a { display: inline-block; padding: 9px 24px; border-radius: 4px;
            border: 1px solid var(--line-strong); color: var(--moon);
            font-size: 14px; font-weight: 600; text-decoration: none;
            transition: background .12s, border-color .12s; }
  .more a:hover { background: var(--ink-3); border-color: var(--ochre);
                  color: var(--moon); text-decoration: none; }
  .intro { line-height: 1.85; color: var(--moon); margin: 28px 0; max-width: 46em; font-size: 16px; }
  .feat h2 { font-family: var(--font-serif); font-size: 24px; margin: 36px 0 18px; font-weight: 600;
             letter-spacing: .04em; }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(140px, 1fr)); gap: 16px; }
  .grid figure { margin: 0; padding: 14px; border-radius: var(--r-l); text-align: center;
                 background: var(--panel); border: 1px solid var(--line); box-shadow: var(--shadow);
                 transition: border-color .12s, transform .12s; }
  .grid figure:hover { border-color: var(--ochre); }
  .grid img { max-width: 112px; max-height: 112px; image-rendering: pixelated; }
  .grid figcaption { font-size: 13px; color: var(--moon-2); margin-top: 8px;
                     overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  footer.site-footer { position: relative; z-index: 1; flex: none; margin-top: 32px; padding: 18px 24px 24px;
           text-align: center; font-size: 12px; color: var(--muted); border-top: 1px solid var(--line); }
  footer.site-footer a { color: var(--jade); text-decoration: none; }
  footer.site-footer a:hover { color: var(--jade-hi); text-decoration: underline; }
  .site-footer-links { display: flex; flex-wrap: wrap; justify-content: center; gap: 6px 18px; margin: 0 0 8px; }
  .site-footer-text { margin: 0 0 6px; white-space: pre-line; }
  .site-footer-credit { margin: 0; }
  /* 品牌标识入场编舞：1600ms 共此时钟，offset 为关键帧内百分比；
     keyframe 缓动一律字面值（var() 在 @keyframes 内会被 Chromium 静默丢弃）。 */
  .brandmark { width: 112px; height: 112px; margin: 0 auto 18px; }
  .brandmark svg { width: 100%; height: 100%; display: block; }
  #p2m-stage, #p2m-plate, #p2m-wipe, #p2m-node-1, #p2m-node-2, #p2m-node-3 { transform-box: fill-box; }
  #p2m-stage, #p2m-plate, #p2m-node-1, #p2m-node-2, #p2m-node-3 { transform-origin: center; }
  #p2m-wipe { transform-origin: left center; }
  #p2m-plate { animation: p2m-plate 1600ms both; }
  @keyframes p2m-plate {
    0% { opacity: 0; transform: scale(0.96); animation-timing-function: cubic-bezier(0.22, 1, 0.36, 1); }
    18% { opacity: 1; transform: scale(1); }
    100% { opacity: 1; transform: scale(1); }
  }
  #p2m-wipe { animation: p2m-wipe 1600ms both; }
  @keyframes p2m-wipe {
    0% { transform: scaleX(0); }
    15% { transform: scaleX(0); animation-timing-function: cubic-bezier(0.65, 0, 0.35, 1); }
    55% { transform: scaleX(1); }
    100% { transform: scaleX(1); }
  }
  #p2m-connector { stroke-dasharray: 1; stroke-dashoffset: 1; animation: p2m-draw 1600ms both; }
  @keyframes p2m-draw {
    0% { stroke-dashoffset: 1; }
    45% { stroke-dashoffset: 1; animation-timing-function: cubic-bezier(0.65, 0, 0.35, 1); }
    70% { stroke-dashoffset: 0; }
    100% { stroke-dashoffset: 0; }
  }
  #p2m-node-1 { animation: p2m-pop1 1600ms both; }
  #p2m-node-2 { animation: p2m-pop2 1600ms both; }
  #p2m-node-3 { animation: p2m-pop3 1600ms both; }
  @keyframes p2m-pop1 {
    0% { transform: scale(0); }
    58% { transform: scale(0); animation-timing-function: cubic-bezier(0.34, 1.56, 0.64, 1); }
    72% { transform: scale(1); }
    100% { transform: scale(1); }
  }
  @keyframes p2m-pop2 {
    0% { transform: scale(0); }
    64% { transform: scale(0); animation-timing-function: cubic-bezier(0.34, 1.56, 0.64, 1); }
    78% { transform: scale(1); }
    100% { transform: scale(1); }
  }
  @keyframes p2m-pop3 {
    0% { transform: scale(0); }
    70% { transform: scale(0); animation-timing-function: cubic-bezier(0.34, 1.56, 0.64, 1); }
    84% { transform: scale(1); }
    100% { transform: scale(1); }
  }
  #p2m-stage { animation: p2m-stage 1600ms both; }
  @keyframes p2m-stage {
    0% { transform: scale(1); }
    78% { transform: scale(1); animation-timing-function: cubic-bezier(0.22, 1, 0.36, 1); }
    84% { transform: scale(1.03); animation-timing-function: cubic-bezier(0.16, 1, 0.3, 1); }
    100% { transform: scale(1); }
  }
  @media (prefers-reduced-motion: reduce) {
    #p2m-stage, #p2m-plate, #p2m-wipe, #p2m-connector, #p2m-node-1, #p2m-node-2, #p2m-node-3 {
      animation: none !important;
      opacity: 1 !important;
      transform: none !important;
      stroke-dashoffset: 0 !important;
    }
    .scene-fog { animation: none !important; }
  }
  @media (max-width: 640px) {
    .wrap { padding: 20px 14px 40px; }
    .hero { padding: 48px 0 28px; }
    .hero h1 { font-size: 32px; }
    .stats { flex-wrap: wrap; gap: 8px; margin: 24px 0; }
    .stats div { min-width: calc(50% - 4px); padding: 16px 8px; }
    .brandmark { width: 88px; height: 88px; margin-bottom: 14px; }
  }
</style>
</head>
<body>
  <!-- 远山云雾（月下仙山之景，纯装饰无语义） -->
  <div class="scene" aria-hidden="true">
    <svg class="scene-mountains" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1440 320" preserveAspectRatio="none">
      <defs>
        <linearGradient id="scene-mist" x1="0" y1="0" x2="0" y2="1">
          <stop offset="0" stop-color="#16202e" stop-opacity="0"/>
          <stop offset="1" stop-color="#16202e" stop-opacity=".55"/>
        </linearGradient>
      </defs>
      <path d="M0,200 L100,162 220,190 340,128 470,172 600,118 730,164 860,108 990,158 1120,124 1260,168 1360,138 1440,162 V320 H0 Z"
            fill="#2c3d54" opacity=".55"/>
      <path d="M0,240 L140,206 300,236 440,190 580,230 720,186 880,226 1030,196 1180,236 1320,200 1440,226 V320 H0 Z"
            fill="#243347" opacity=".8"/>
      <path d="M0,280 L160,256 330,276 520,250 700,272 900,248 1090,270 1280,250 1440,268 V320 H0 Z"
            fill="#1c2839"/>
      <rect x="0" y="0" width="1440" height="320" fill="url(#scene-mist)"/>
    </svg>
    <div class="scene-fog fog-a"></div>
    <div class="scene-fog fog-b"></div>
  </div>
  <div class="wrap">
    <header class="hero">
      <div class="brandmark" aria-hidden="true">
<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 512 512" width="512" height="512">
  <defs>
    <linearGradient id="p2m-g-plate" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0" stop-color="#ffffff"/>
      <stop offset="1" stop-color="#eef3fb"/>
    </linearGradient>
    <linearGradient id="p2m-g-n" x1="0" y1="0" x2="1" y2="1">
      <stop offset="0" stop-color="#2f66ff"/>
      <stop offset="0.55" stop-color="#2a8fe0"/>
      <stop offset="1" stop-color="#20b983"/>
    </linearGradient>
    <filter id="p2m-soft" x="-20%" y="-20%" width="140%" height="140%">
      <feDropShadow dx="0" dy="10" stdDeviation="18" flood-color="#2f66ff" flood-opacity="0.18"/>
    </filter>
    <mask id="p2m-wipe-mask">
      <rect id="p2m-wipe" x="96" y="76" width="420" height="380" fill="#ffffff"/>
    </mask>
  </defs>
  <g id="p2m-stage">
    <rect id="p2m-plate" x="36" y="36" width="440" height="440" rx="96"
          fill="url(#p2m-g-plate)" stroke="#d3def8" stroke-width="2"/>
    <g id="p2m-mark" mask="url(#p2m-wipe-mask)" filter="url(#p2m-soft)">
      <path d="M 128 400 V 112 h 80 l 98 150 V 112 h 80 v 288 h -80 l -98 -150 v 150 z" fill="url(#p2m-g-n)"/>
      <rect x="128" y="112" width="80" height="80" fill="#ffffff" opacity="0.12"/>
      <rect x="306" y="320" width="80" height="80" fill="#ffffff" opacity="0.10"/>
    </g>
    <path id="p2m-connector" d="M 168 248 V 178 L 282 262 H 346 V 330"
          stroke="#ffffff" stroke-opacity="0.55" stroke-width="7" fill="none"
          stroke-linecap="butt" pathLength="1"/>
    <rect id="p2m-node-1" x="152" y="150" width="32" height="32" rx="6" fill="#ffffff" opacity="0.92"/>
    <rect id="p2m-node-2" x="266" y="246" width="32" height="32" rx="6" fill="#ffffff" opacity="0.92"/>
    <rect id="p2m-node-3" x="330" y="330" width="32" height="32" rx="6" fill="#eafff6" opacity="0.92"/>
  </g>
</svg>
      </div>
      <h1>{{.SiteName}}</h1>
      <p class="tag">Minecraft 皮肤站，支持 Yggdrasil 外置登录</p>
      <div class="cta">
        {{if .LoggedIn}}<a class="primary" href="/user/dashboard">进入用户中心</a>
        <a class="alt" href="/skinlib">浏览皮肤库</a>{{else}}<a class="primary" href="/register">注册账号</a>
        <a class="alt" href="/login">登录</a>{{end}}
      </div>
    </header>
    <section class="stats">
      <div><b>{{.Users}}</b><span>注册用户</span></div>
      <div><b>{{.Players}}</b><span>游戏角色</span></div>
      <div><b>{{.Textures}}</b><span>皮肤材质</span></div>
    </section>
    <div class="divider" aria-hidden="true"></div>
    <section class="intro"><p>{{.Intro}}</p></section>
    {{if .Featured}}
    <section class="feat">
      <h2>皮肤库精选</h2>
      <div class="grid">
        {{range .Featured}}
        <figure><img src="{{.URL}}" alt="{{.Name}}" loading="lazy" /><figcaption>{{.Name}}</figcaption></figure>
        {{end}}
      </div>
      <p class="more"><a href="/skinlib">进入皮肤库</a></p>
    </section>
    {{end}}
  </div>
  {{if .FooterHTML}}<footer class="site-footer">{{.FooterHTML}}</footer>{{end}}
  {{.BodyAnchor}}
</body>
</html>
`))

// pageMetaFor: SPA 壳统一 noindex（2026-10-27 逻辑修复：删除 /profile/{name}
// 假内容页 SEO 残留 —— SPA 根本没有该路由，壳是登录墙/工具页，不该进索引；
// 真内容页（landing/详情页/皮肤库）由各自渲染层自带 meta）。
func pageMetaFor(p string) seo.PageMeta {
	return seo.PageMeta{NoIndex: true}
}

// Injection anchors are passed as trusted template.HTML data: html/template
// strips HTML comments written in template source, which silently deleted the
// <!--skin-*--> markers and left the SEO/theme/custom blocks unanchored
// (2026-10-25 production regression, reproduced in tests).
const (
	landingHeadAnchors = "<!--skin-seo-->\n<!--skin-theme-->\n<!--skin-custom-head-->"
	landingBodyAnchor  = "<!--skin-custom-body-->"
)

type landingView struct {
	landingData
	HeadAnchors template.HTML
	BodyAnchor  template.HTML
}

// renderLanding renders the landing page through the same SEO + appearance
// injection layers as the SPA shell.
func renderLanding(ctx context.Context, seoEng *seo.Engine, appearEng *appearance.Engine, d landingData) string {
	d.Year = time.Now().Year()
	d.FooterHTML = appearEng.FooterData(ctx, d.SiteName, d.SiteURL, d.Year).HTML()
	d.Intro = landingIntro
	var sb strings.Builder
	v := landingView{
		landingData: d,
		HeadAnchors: template.HTML(landingHeadAnchors),
		BodyAnchor:  template.HTML(landingBodyAnchor),
	}
	if err := landingTmpl.Execute(&sb, v); err != nil {
		return "<!DOCTYPE html><html><body><h1>landing render error</h1></body></html>"
	}
	html := seoEng.RenderHeadWith(ctx, sb.String(), seo.PageMeta{Description: landingIntro})
	return appearEng.Render(ctx, html)
}
