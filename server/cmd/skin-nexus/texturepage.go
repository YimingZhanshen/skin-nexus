package main

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
)

// texDetailData feeds the detail template.
type texDetailData struct {
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
	Year          int

	HeadAnchors template.HTML
	BodyAnchor  template.HTML
	PluginSlot  template.HTML
}

var texDetailTmpl = template.Must(template.New("texdetail").Parse(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
<link rel="icon" type="image/svg+xml" href="/favicon.svg" />
<link rel="icon" type="image/x-icon" href="/favicon.ico" sizes="48x48" />
<link rel="apple-touch-icon" href="/apple-touch-icon.png" />
<link rel="manifest" href="/manifest.webmanifest" />
<meta name="theme-color" content="#16202e" />
{{.HeadAnchors}}
<style>
  /* 详情页设计语言 v3「月下仙山」（与落地页同 token 命名空间；
     主题/custom CSS 在其后注入仍可整体覆盖，见 theme-spec v3 节） */
  :root {
    color-scheme: dark;
    --font-serif: "Noto Serif SC", "Source Han Serif SC", "Songti SC", "SimSun", serif;
    --ink: #16202e; --ink-2: #1c2839; --ink-3: #243347;
    --line: rgba(169, 138, 95, .30);
    --line-strong: rgba(169, 138, 95, .45);
    --moon: #e9edf1; --moon-2: #aab6c4;
    --cinnabar: #b0453a; --cinnabar-hi: #c25446; --on-cinnabar: #fdf3ec;
    --jade: #67b7a0; --jade-hi: #7fcbb3;
    --gold: #c9a06a;
    --danger: #d9564c;
    --sel: rgba(176, 69, 58, .35);
    --ring-cinnabar: rgba(176, 69, 58, .18);
    --glow-cinnabar: rgba(176, 69, 58, .6);
  }
  * { box-sizing: border-box; }
  body { margin: 0; font-family: "Segoe UI", "Inter", "PingFang SC", "HarmonyOS Sans SC", "Noto Sans CJK SC", "Microsoft YaHei", sans-serif;
         font-size: 15px; line-height: 1.7; -webkit-font-smoothing: antialiased; line-break: strict;
         background: var(--ink); color: var(--moon); }
  ::selection { background: var(--sel); }
  a { color: var(--jade); text-decoration: none; }
  a:hover { color: var(--jade-hi); text-decoration: underline; }
  :focus-visible { outline: 2px solid var(--jade); outline-offset: 2px; }
  .wrap { max-width: 760px; margin: 0 auto; padding: 24px 16px 48px; }
  .crumb { font-size: 13px; color: var(--moon-2); margin: 8px 0 18px; }
  .card { background: var(--ink-2); border: 1px solid var(--line);
          border-radius: 10px; padding: 24px;
          box-shadow: 0 12px 32px -18px rgba(3, 8, 16, .8); }
  h1 { margin: 0 0 4px; font-family: var(--font-serif); font-size: 26px; font-weight: 600;
       letter-spacing: .03em; word-break: break-all; }
  .sub { color: var(--moon-2); font-size: 14px; margin: 0 0 20px; }
  .viewer { display: flex; justify-content: center; align-items: center;
            min-height: 300px; margin: 0 0 20px; position: relative; }
  .viewer img.static { max-height: 280px; image-rendering: pixelated; }
  .viewer canvas { display: none; }
  .viewer.ready img.static { display: none; }
  .viewer.ready canvas { display: block; }
  .meta { display: grid; grid-template-columns: repeat(auto-fill, minmax(150px, 1fr));
          gap: 8px; margin: 0 0 20px; }
  .meta div { position: relative; background: var(--ink-3); border: 1px solid var(--line);
              border-radius: 6px; padding: 10px 12px; }
  /* 赭石回纹角饰（与落地页统计卡同款小音量） */
  .meta div::before, .meta div::after { content: ""; position: absolute; width: 8px; height: 8px;
              border: 1.5px solid var(--gold); opacity: .55; }
  .meta div::before { top: 5px; left: 5px; border-right: none; border-bottom: none; }
  .meta div::after { bottom: 5px; right: 5px; border-left: none; border-top: none; }
  .meta dt { font-size: 12px; color: var(--moon-2); margin: 0 0 2px; }
  .meta dd { margin: 0; font-size: 14px; font-weight: 600; word-break: break-all;
             font-variant-numeric: tabular-nums; }
  .actions { display: flex; gap: 8px; flex-wrap: wrap; align-items: center; }
  .actions a, .actions button { display: inline-block; padding: 9px 20px; border-radius: 4px;
        font-size: 14px; font-weight: 600; text-decoration: none; cursor: pointer;
        font-family: inherit; transition: background .14s cubic-bezier(.22,1,.36,1),
        border-color .14s cubic-bezier(.22,1,.36,1), transform .14s cubic-bezier(.22,1,.36,1),
        box-shadow .14s cubic-bezier(.22,1,.36,1); }
  .actions .primary { background: var(--cinnabar); color: var(--on-cinnabar); border: none; }
  .actions .primary:hover { background: var(--cinnabar-hi); transform: translateY(-1px);
        box-shadow: 0 6px 16px -8px var(--glow-cinnabar); text-decoration: none; }
  .actions .primary:active { transform: translateY(0); }
  .actions .alt { border: 1px solid var(--line-strong); color: var(--moon);
                  background: transparent; }
  .actions .alt:hover { background: var(--ink-3); border-color: var(--gold);
                  color: var(--moon); text-decoration: none; }
  .like-btn.liked { color: var(--danger); border-color: var(--danger); }
  footer { position: relative; margin-top: 32px; padding-top: 16px; text-align: center;
           font-size: 12px; color: var(--moon-2); border-top: 1px solid var(--line); }
  footer a { color: var(--gold); }
  .detail-block { margin-top: 18px; padding-top: 14px; border-top: 1px solid var(--border); }
  .detail-block h2 { font-size: var(--fs-m); margin: 0 0 8px; }
  .source-line { margin: 0 0 8px; color: var(--muted); font-size: var(--fs-s); }
  .source-line a { color: var(--jade); }
  .report-dialog { border: 1px solid var(--border); border-radius: var(--r-m); background: var(--panel);
    color: var(--text); max-width: 420px; width: calc(100% - 32px); padding: 18px; }
  .report-dialog label { display: block; margin: 10px 0; font-size: var(--fs-s); }
  .report-dialog select, .report-dialog textarea { width: 100%; margin-top: 4px; }
  .report-actions { display: flex; gap: 8px; justify-content: flex-end; margin-top: 12px; }
  .report-error { color: var(--danger, #d9564c); font-size: var(--fs-s); }
  @media (prefers-reduced-motion: reduce) { * { animation: none !important; transition: none !important; } }
  @media (max-width: 560px) { h1 { font-size: 21px; } .viewer { min-height: 240px; } }
</style>
<!--skin-theme-->
<!--skin-custom-head-->
</head>
<body>
  <div class="wrap">
    <nav class="crumb"><a href="/">首页</a> › <a href="/skinlib">皮肤库</a> › {{.Name}}</nav>
    <article class="card" itemscope itemtype="https://schema.org/CreativeWork">
      <h1 itemprop="name">{{.Name}}</h1>
      <p class="sub">Minecraft {{if eq .Type "cape"}}披风{{else}}皮肤{{end}} · 上传者 <span itemprop="author">{{.Nickname}}</span></p>
      <div class="viewer" id="tex-viewer">
        <img class="static" src="{{.PreviewURL}}" alt="{{.Name}} 的 Minecraft {{if eq .Type "cape"}}披风{{else}}皮肤{{end}} 预览图" itemprop="image" />
        {{if eq .Type "skin"}}<canvas data-skinview3d data-texture="{{.ViewerURL}}" data-model="{{.Model}}" width="320" height="420"></canvas>{{end}}
      </div>
      <dl class="meta">
        <div><dt>类型</dt><dd>{{if eq .Type "cape"}}披风{{else if eq .Model "slim"}}皮肤（Alex 瘦臂）{{else}}皮肤（Steve 常规）{{end}}</dd></div>
        <div><dt>材质 ID</dt><dd>#{{.TID}}</dd></div>
        <div><dt>喜欢</dt><dd><span id="like-count">{{.Likes}}</span> 人</dd></div>
        {{if .UploadAt}}<div><dt>上传时间</dt><dd>{{.UploadAt}}</dd></div>{{end}}
        <div><dt>可见性</dt><dd>{{if .Public}}公开{{else}}私密{{end}}</dd></div>
      </dl>
      {{if or .DetailHTML .SourceLabel}}
      <section class="detail-block">
        <h2>材质描述</h2>
        {{if .SourceLabel}}<p class="source-line">来源：{{if .SourceURL}}<a href="{{.SourceURL}}" target="_blank" rel="nofollow noopener">{{.SourceLabel}}</a>{{else}}{{.SourceLabel}}{{end}}</p>{{end}}
        {{if .DetailHTML}}<div class="md">{{.DetailHTML}}</div>{{end}}
      </section>
      {{end}}
      <div class="actions">
        <button class="alt like-btn{{if .Liked}} liked{{end}}" data-like="{{.TID}}" data-liked="{{if .Liked}}1{{else}}0{{end}}">♥ <span>喜欢 / 加入衣柜</span></button>
        <a class="primary" href="{{.TextureURL}}" download>下载纹理 PNG</a>
        <a class="alt" href="/register">注册使用同款</a>
        {{if .Public}}<button class="alt report-btn" id="report-btn" data-tid="{{.TID}}">举报</button>{{end}}
      </div>
    </article>
    {{.PluginSlot}}
  </div>
  <footer>
    Powered by <a href="https://github.com/YimingZhanshen/skin-nexus">SkinNexus</a>
    · © {{.Year}} {{.SiteName}}
  </footer>
  <dialog id="report-dialog" class="report-dialog">
    <form id="report-form" method="dialog">
      <h2>举报材质</h2>
      <label>原因
        <select id="report-reason">
          <option value="copyright">侵权 / 无授权转载</option>
          <option value="sexual">色情或擦边内容</option>
          <option value="violence">暴力或血腥</option>
          <option value="hate">仇恨或歧视</option>
          <option value="spam">垃圾或误导信息</option>
          <option value="other">其他</option>
        </select>
      </label>
      <label>补充说明（选填）
        <textarea id="report-detail" rows="3" maxlength="500"></textarea>
      </label>
      <p class="report-error" id="report-error" hidden></p>
      <div class="report-actions">
        <button type="button" class="alt" id="report-cancel">取消</button>
        <button type="submit" value="submit">提交举报</button>
      </div>
    </form>
  </dialog>
  <script type="module" src="/assets/viewer.js?v=1"></script>
  {{.BodyAnchor}}
</body>
</html>
`))

// texNotFoundTmpl is the minimal branded 404 for missing/private textures —
// a plain http.NotFound would drop the theme and look like a server fault.
var texNotFoundTmpl = template.Must(template.New("tex404").Parse(`<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="UTF-8" />
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
<title>材质不存在 - {{.SiteName}}</title>
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
<body><div class="box"><h1>404</h1><p>{{.Msg}}</p><p><a href="/">返回首页</a></p></div></body>
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
		tid, err := strconv.ParseInt(r.PathValue("tid"), 10, 64)
		if err != nil || tid <= 0 {
			writeTexNotFound(r.Context(), w, siteName, appearEng, "材质不存在，或已被设为私密。")
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
			writeTexNotFound(r.Context(), w, siteName, appearEng, "材质不存在，或已被设为私密。")
			return
		}
		if err != nil {
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}

		d := texDetailData{
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
			SourceLabel: sourceLabel(row.Source),
		}
		if d.Name == "" {
			d.Name = "材质 #" + strconv.FormatInt(row.TID, 10)
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
			TitleSuffix: d.Name + " 的 Minecraft " + texKindLabel(row.Type),
			Description: texDescription(d),
			Path:        "/skinlib/" + strconv.FormatInt(row.TID, 10),
			OGImage:     d.PreviewAbsURL,
			JSONLD:      texJSONLD(d, canonical),
		}
		// Private content viewed by its owner must never be indexed.
		pm.NoIndex = !row.Public

		var sb strings.Builder
		d.Year = time.Now().Year()
		d.HeadAnchors = template.HTML(landingHeadAnchors)
		d.BodyAnchor = template.HTML(landingBodyAnchor)
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

func texKindLabel(t string) string {
	if t == "cape" {
		return "披风"
	}
	return "皮肤"
}

func texDescription(d texDetailData) string {
	kind := texKindLabel(d.Type)
	model := ""
	if d.Type == "skin" {
		if d.Model == "slim" {
			model = "，Alex 瘦臂模型"
		} else {
			model = "，Steve 常规模型"
		}
	}
	intro := d.Name
	if d.Summary != "" {
		intro += "：" + d.Summary
	}
	return intro + " — " + d.SiteName + "皮肤库中的 Minecraft " + kind + model +
		"，由 " + d.Nickname + " 上传，" + strconv.FormatInt(d.Likes, 10) +
		" 人喜欢。注册账号即可一键使用。"
}

// writeTexNotFound renders the branded 404 with theme/custom-css injection
// (the anchors above feed appearEng.Render) and a true 404 status. msg is the
// situation line (texture vs generic page wording).
func writeTexNotFound(ctx context.Context, w http.ResponseWriter, siteName string, appearEng *appearance.Engine, msg string) {
	var sb strings.Builder
	_ = texNotFoundTmpl.Execute(&sb, map[string]string{"SiteName": siteName, "Msg": msg})
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusNotFound)
	html := sb.String()
	if appearEng != nil {
		html = appearEng.Render(ctx, html)
	}
	_, _ = w.Write([]byte(html))
}

func sourceLabel(source string) string {
	switch source {
	case "original":
		return "原创"
	case "derivative":
		return "二创"
	case "repost":
		return "转载"
	default:
		return ""
	}
}
