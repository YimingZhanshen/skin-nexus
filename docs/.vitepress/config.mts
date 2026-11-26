import { existsSync } from 'node:fs'
import { dirname, isAbsolute, relative, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { defineConfig } from 'vitepress'
import { withMermaid } from 'vitepress-plugin-mermaid'
import { chineseSearchOptimize, pagefindPlugin } from 'vitepress-plugin-pagefind'
import llmstxt from 'vitepress-plugin-llms'

// 文档站配置：docs.skin.yimingzhanshen.cn（Cloudflare Pages 托管，CF 管 TLS）。
// 内容 = 契约主目录（deploy/migration/build/spec…）+ handbook/ 知识手册（80 篇）。
// SEO 是本站主因：静态渲染 + sitemap + llms.txt（AI 答案引擎入口）。
const HOST = 'https://docs.skin.yimingzhanshen.cn'
const DOCS_ROOT = fileURLToPath(new URL('../', import.meta.url))
const REPOSITORY_ROOT = fileURLToPath(new URL('../..', import.meta.url))
const REPOSITORY_SOURCE = 'https://github.com/YimingZhanshen/skin-nexus/blob/main/'

export default withMermaid(
  defineConfig({
    lang: 'zh-CN',
    title: 'SkinNexus 文档',
    titleTemplate: ':title | SkinNexus 文档',
    description:
      'Self-hosted Minecraft skin server documentation — deploy a Go skin server with Yggdrasil external auth and migrate losslessly from blessing-skin-server.',
    base: '/',
    cleanUrls: true,
    lastUpdated: true,
    head: [
      ['link', { rel: 'sitemap', type: 'application/xml', href: '/sitemap.xml' }],
      ['meta', { property: 'og:image', content: HOST + '/og-image.png' }],
      ['meta', { property: 'og:image:width', content: '1200' }],
      ['meta', { property: 'og:image:height', content: '630' }],
      ['meta', { name: 'twitter:image', content: HOST + '/og-image.png' }],
    ],
    // GEO/AEO：每页补 OG/Twitter meta；首页挂 SoftwareApplication+Organization
    // JSON-LD；手册 FAQ 页挂 FAQPage JSON-LD（问题/答案与页面可见问答逐条对齐，
    // 搜索引擎要求标记内容与可见内容一致）。og:image 全站一张 = /og-image.png
    //（1200×630，public/ 根路径），Twitter 卡升 summary_large_image。
    transformHead({ pageData, title, description }) {
      const rel = pageData.relativePath.replace(/\.md$/, '').replace(/(^|\/)index$/, '$1')
      const url = HOST + '/' + rel
      const desc =
        description ||
        'Self-hosted Minecraft skin server documentation — deploy a Go skin server with Yggdrasil external auth and migrate losslessly from blessing-skin-server.'
      const head: any[] = [
        ['meta', { property: 'og:title', content: title || 'SkinNexus 文档' }],
        ['meta', { property: 'og:description', content: desc }],
        ['meta', { property: 'og:url', content: url }],
        ['meta', { property: 'og:site_name', content: 'SkinNexus 文档' }],
        ['meta', { property: 'og:type', content: 'website' }],
        ['meta', { property: 'og:locale', content: 'zh_CN' }],
        ['meta', { name: 'twitter:card', content: 'summary_large_image' }],
      ]
      if (pageData.relativePath === 'index.md') {
        head.push([
          'script',
          { type: 'application/ld+json' },
          JSON.stringify({
            '@context': 'https://schema.org',
            '@type': 'SoftwareApplication',
            name: 'SkinNexus',
            applicationCategory: 'GameApplication',
            operatingSystem: 'Linux (Docker)',
            description: desc,
            url: HOST + '/',
            codeRepository: 'https://github.com/YimingZhanshen/skin-nexus',
            license: 'https://www.gnu.org/licenses/agpl-3.0.html',
            author: { '@type': 'Person', name: 'YMZS' },
          }),
        ])
        head.push([
          'script',
          { type: 'application/ld+json' },
          JSON.stringify({
            '@context': 'https://schema.org',
            '@type': 'Organization',
            name: 'YMZS',
            url: 'https://github.com/YimingZhanshen',
          }),
        ])
      }
      if (pageData.relativePath === 'handbook/故障排除与常见问题.md') {
        const q = (name: string, text: string) => ({
          '@type': 'Question',
          name,
          acceptedAnswer: { '@type': 'Answer', text },
        })
        head.push([
          'script',
          { type: 'application/ld+json' },
          JSON.stringify({
            '@context': 'https://schema.org',
            '@type': 'FAQPage',
            mainEntity: [
              q(
                'SkinNexus 管理员或玩家无法登录怎么办',
                '常见现象是提交账号密码后页面没有跳转或提示认证失败。逐项排查：新站未初始化需用一次性安装令牌（SKIN_SETUP_TOKEN 或容器日志）完成 /api/v1/setup；迁移来的账号若老站 password 列为空会被拒绝，需超管重置或走找回密码；邮箱验证门开启且宽限期已过会拦下未验证账号，应先配 SMTP、再设宽限期、最后开门；短时间内重复登录失败会触发认证限流（429），查访问日志来源 IP。',
              ),
              q(
                'SkinNexus 收不到验证邮件或找回密码信怎么办',
                '完全收不到信先查 SMTP 主机/端口/用户名/密码/发件人配置（部署自检 mail 项）；进垃圾箱多因缺 SPF 或 DMARC，需在域名 DNS 补记录（部署自检 mail_dns 项）；偶发失败会自动重试一次，持续失败查审计日志中的 mail_test_failed 等事件。发信失败会写入审计并在运行状态的 24 小时发信失败计数中体现。',
              ),
              q(
                'SkinNexus 皮肤上传失败怎么排查',
                '上传后文件不在磁盘：核对 SKIN_TEXTURES_DIR 与数据卷挂载（fsck 报 missing files）；存在孤立文件：fsck 先 dry-run 确认后 fix（默认只报告不删除）；纹理渲染失败：重新上传正确分辨率的皮肤；SPA 页面空白：部署 web/dist 前端构建产物或使用 Docker 镜像自带静态资源。',
              ),
              q(
                'SkinNexus 管理面板空白怎么办',
                'API 能访问但管理界面空白，多为静态资源目录不存在——自助诊断 assets 项会报 fail 并给出 index.html 路径，部署 web/dist 或换 Docker 镜像即可；仅管理路由异常则查管理员权限、会话与反代透传（访问日志 401/403）。',
              ),
              q(
                '启动器连接 SkinNexus 进服报错怎么办',
                'authlib-injector 或 yggc/OIDC 启动器报认证失败：发现文档找不到（404/无 CORS）时确保 /api/yggdrasil 规范地址直接返回 200 且无重复 Access-Control-Allow-Origin；选角后 403 查启动器版本与 join 请求日志；OIDC 免密登录按钮不出现查 discovery 与 shared_client_id 是否暴露。',
              ),
              q(
                'SkinNexus 登录后皮肤不显示怎么办',
                '本地能看到而玩家看不到：site_url/base_url 解析成了内网地址，设置公网地址后重启（自助诊断 site 项会警告）；联盟成员站名称消失：反代（如宝塔 WAF 跨域设置）重复注入 CORS 头，关闭后恢复；带端口域名无法匹配 skinDomains：改用无端口域名（系统会为带端口条目追加去端口孪生）。',
              ),
              q(
                'SkinNexus 角色改名后 UUID 丢失或联盟站角色不同步怎么办',
                'UUID 零漂移是铁律：改名后 UUID 应始终不变，若变化需检查迁移流程与外部系统是否误算 OfflinePlayer UUID。联盟站角色未更新：查审计日志的 union_sync_*_failed 事件，在管理面板「同步角色数据」手动收敛；自删账号不同步查 hub 回探连通性与签名密钥。',
              ),
            ],
          }),
        ])
      }
      return head
    },
    markdown: {
      lineNumbers: false,
      config(md) {
        // handbook 的源码引用指向 docs/ 外的 web/ 与 server/。VitePress 只把站内
        // 页面视为有效目标；先在本地确认文件存在，再改为 GitHub 源码链接，既不
        // 关闭死链检查，也不让静态站生成指向 /web 或 /server 的失效 URL。
        const renderLink = md.renderer.rules.link_open!
        md.renderer.rules.link_open = (tokens, idx, options, env, self) => {
          const href = tokens[idx].attrGet('href')
          if (href && !/^(?:[a-z][a-z\d+.-]*:|\/|#)/i.test(href)) {
            const hashIndex = href.indexOf('#')
            const filePart = hashIndex === -1 ? href : href.slice(0, hashIndex)
            const hash = hashIndex === -1 ? '' : href.slice(hashIndex)
            const sourcePath = resolve(dirname(env.path), filePart)
            const fromDocs = relative(DOCS_ROOT, sourcePath)
            const fromRepository = relative(REPOSITORY_ROOT, sourcePath)
            if (
              fromDocs.startsWith('..') &&
              !fromRepository.startsWith('..') &&
              !isAbsolute(fromRepository) &&
              existsSync(sourcePath)
            ) {
              tokens[idx].attrSet(
                'href',
                REPOSITORY_SOURCE + fromRepository.replace(/\\/g, '/') + hash,
              )
            }
          }
          return renderLink(tokens, idx, options, env, self)
        }
      },
    },
    themeConfig: {
      editLink: {
        pattern: 'https://github.com/YimingZhanshen/skin-nexus/edit/main/docs/:path',
        text: '在 GitHub 上编辑此页',
      },
      nav: [
        { text: '部署', link: '/deploy' },
        { text: '迁移', link: '/migration' },
        { text: '知识手册', link: '/handbook/index' },
        { text: '插件规范', link: '/plugin-spec' },
        { text: '主题规范', link: '/theme-spec' },
        { text: '启动器 API', link: '/launcher-api' },
        { text: '变更日志', link: '/CHANGELOG' },
      ],
      sidebar: [
        {
          text: '契约与部署',
          items: [
            { text: '部署运维', link: '/deploy' },
            { text: '服务器接入', link: '/server-access' },
            { text: '从 blessing-skin-server 迁移', link: '/migration' },
            { text: '构建与 CI', link: '/build' },
            { text: '兼容矩阵', link: '/compat-matrix' },
            { text: '插件规范', link: '/plugin-spec' },
            { text: '插件发布', link: '/plugin-publishing' },
            { text: '主题规范', link: '/theme-spec' },
            { text: '启动器 API', link: '/launcher-api' },
            { text: '竞品源码分析', link: '/competitor-analysis' },
            { text: '变更日志', link: '/CHANGELOG' },
          ],
        },
        {
          text: '知识手册 · 入门与架构',
          collapsed: true,
          items: [
            { text: '手册总览', link: '/handbook/index' },
            { text: '项目概览与快速开始', link: '/handbook/项目概览与快速开始' },
            { text: '快速开始', link: '/handbook/快速开始' },
            { text: '开发指南', link: '/handbook/开发指南' },
            { text: '故障排除与常见问题', link: '/handbook/故障排除与常见问题' },
            { text: '系统架构总览', link: '/handbook/系统架构总览' },
            { text: '后端核心模块', link: '/handbook/后端核心模块' },
            { text: '应用入口与外壳', link: '/handbook/应用入口与外壳' },
            { text: '路由系统与 API 暴露', link: '/handbook/路由系统与-API-暴露' },
            { text: '数据模型', link: '/handbook/数据模型' },
            { text: '状态管理与领域模型', link: '/handbook/状态管理与领域模型' },
          ],
        },
        {
          text: '知识手册 · 存储与迁移',
          collapsed: true,
          items: [
            { text: '多引擎存储抽象模块', link: '/handbook/多引擎存储抽象模块' },
            { text: '方言适配与 dbx 抽象层', link: '/handbook/方言适配与-dbx-抽象层' },
            { text: '数据库与迁移', link: '/handbook/数据库与迁移' },
            { text: '数据库选型与迁移', link: '/handbook/数据库选型与迁移' },
            { text: '迁移系统', link: '/handbook/迁移系统' },
          ],
        },
        {
          text: '知识手册 · 账号与协议',
          collapsed: true,
          items: [
            { text: '认证与账户系统', link: '/handbook/认证与账户系统' },
            { text: '本地账号与密码认证', link: '/handbook/本地账号与密码认证' },
            { text: '密码与认证模块', link: '/handbook/密码与认证模块' },
            { text: '账户与角色模块', link: '/handbook/账户与角色模块' },
            { text: '用户管理', link: '/handbook/用户管理' },
            { text: '玩家与角色管理', link: '/handbook/玩家与角色管理' },
            { text: '邀请码与注册门禁', link: '/handbook/邀请码与注册门禁' },
            { text: '邮箱验证门禁与通知', link: '/handbook/邮箱验证门禁与通知' },
            { text: 'Yggdrasil 协议实现', link: '/handbook/Yggdrasil-协议实现' },
            { text: 'Yggdrasil 协议 API', link: '/handbook/Yggdrasil-协议-API' },
            { text: '内置 OIDC 提供者', link: '/handbook/内置-OIDC-提供者' },
            { text: 'OIDC 提供者模块', link: '/handbook/OIDC-提供者模块' },
            { text: 'OIDC 第三方登录集成', link: '/handbook/OIDC-第三方登录集成' },
            { text: '启动器 Bearer API', link: '/handbook/启动器-Bearer-API' },
            { text: '协议与外部集成', link: '/handbook/协议与外部集成' },
          ],
        },
        {
          text: '知识手册 · 材质与皮肤库',
          collapsed: true,
          items: [
            { text: '纹理上传与处理管线', link: '/handbook/纹理上传与处理管线' },
            { text: '纹理处理与材质模块', link: '/handbook/纹理处理与材质模块' },
            { text: '皮肤与材质管理', link: '/handbook/皮肤与材质管理' },
            { text: '材质管理', link: '/handbook/材质管理' },
            { text: '皮肤库浏览与筛选', link: '/handbook/皮肤库浏览与筛选' },
            { text: '头像生成与材质元数据', link: '/handbook/头像生成与材质元数据' },
          ],
        },
        {
          text: '知识手册 · 插件与主题',
          collapsed: true,
          items: [
            { text: '插件系统', link: '/handbook/插件系统' },
            { text: '插件架构与生命周期', link: '/handbook/插件架构与生命周期' },
            { text: '插件管理', link: '/handbook/插件管理' },
            { text: '插件开发指南', link: '/handbook/插件开发指南' },
            { text: '插件 API', link: '/handbook/插件-API（-api-plugins---v1）' },
            { text: 'Manifest v2 规范', link: '/handbook/Manifest-v2-规范' },
            { text: '事件总线与钩子', link: '/handbook/事件总线与钩子' },
            { text: 'UI 插槽与 iframe 集成', link: '/handbook/UI-插槽与-iframe-集成' },
            { text: '主题系统', link: '/handbook/主题系统' },
            { text: '主题市场', link: '/handbook/主题市场' },
          ],
        },
        {
          text: '知识手册 · API 参考',
          collapsed: true,
          items: [
            { text: 'API 参考', link: '/handbook/API-参考' },
            { text: 'API 客户端封装', link: '/handbook/API-客户端封装' },
            { text: 'Web API', link: '/handbook/Web-API（-api-v1）' },
            { text: '管理 API', link: '/handbook/管理-API（-api-admin）' },
            { text: '公开只读 API', link: '/handbook/公开只读-API（-api-public-v1）' },
          ],
        },
        {
          text: '知识手册 · 前端与面板',
          collapsed: true,
          items: [
            { text: '前端架构', link: '/handbook/前端架构' },
            { text: '组件体系', link: '/handbook/组件体系' },
            { text: '页面与路由', link: '/handbook/页面与路由' },
            { text: '国际化', link: '/handbook/国际化' },
            { text: '管理面板', link: '/handbook/管理面板' },
            { text: '面板概览与导航', link: '/handbook/面板概览与导航' },
            { text: '仪表盘', link: '/handbook/仪表盘' },
            { text: '站点设置', link: '/handbook/站点设置' },
            { text: '举报、通知与任务', link: '/handbook/举报、通知与任务' },
          ],
        },
        {
          text: '知识手册 · 运维与安全',
          collapsed: true,
          items: [
            { text: '部署与运维', link: '/handbook/部署与运维' },
            { text: 'Docker 部署', link: '/handbook/Docker-部署' },
            { text: '构建与开发环境', link: '/handbook/构建与开发环境' },
            { text: '二进制与源码构建', link: '/handbook/二进制与源码构建' },
            { text: '环境变量参考', link: '/handbook/环境变量参考' },
            { text: '反向代理配置', link: '/handbook/反向代理配置' },
            { text: '备份与恢复', link: '/handbook/备份与恢复' },
            { text: '备份与灾难恢复', link: '/handbook/备份与灾难恢复' },
            { text: '监控与健康检查', link: '/handbook/监控与健康检查' },
            { text: '性能与可扩展性', link: '/handbook/性能与可扩展性' },
            { text: '安全与合规', link: '/handbook/安全与合规' },
            { text: '安全模型与沙箱限制', link: '/handbook/安全模型与沙箱限制' },
            { text: '审计与访问日志', link: '/handbook/审计与访问日志' },
            { text: '访问日志与审计模块', link: '/handbook/访问日志与审计模块' },
            { text: '出站请求安全封装', link: '/handbook/出站请求安全封装' },
            { text: '测试与质量保障', link: '/handbook/测试与质量保障' },
          ],
        },
        {
          text: '知识手册 · 功能专题',
          collapsed: true,
          items: [
            { text: 'UGC 文本过滤与审核举报', link: '/handbook/UGC-文本过滤与审核举报' },
            { text: '后台任务模块', link: '/handbook/后台任务模块' },
            { text: '邮件服务集成', link: '/handbook/邮件服务集成' },
          ],
        },
      ],
      outline: { level: [2, 3], label: '本页目录' },
      footer: { message: 'AGPL-3.0 · SkinNexus', link: 'https://github.com/YimingZhanshen/skin-nexus' },
    },
    // 死链逐条修，不用 ignoreDeadLinks 一关了之；构建报错即清单。
    ignoreDeadLinks: false,
    vite: {
      plugins: [
        // Pagefind 中文搜索（Intl.Segmenter 分词）+ llms.txt（AI 答案引擎入口）
        pagefindPlugin({ customSearchQuery: chineseSearchOptimize }),
        llmstxt(),
      ],
    },
    // sitemap 无可靠现成插件，buildEnd 十几行自生成（cleanUrls 口径）
    async buildEnd({ outDir }) {
      const { readdirSync, statSync, writeFileSync, readFileSync } = await import('node:fs')
      const { join, relative } = await import('node:path')
      const urls: string[] = []
      const walk = (d: string) => {
        for (const e of readdirSync(d)) {
          const p = join(d, e)
          if (statSync(p).isDirectory()) walk(p)
          else if (e.endsWith('.html')) {
            let u = '/' + relative(outDir, p).split('\\').join('/').replace(/\.html$/, '')
            u = u.replace(/\/index$/, '/')
            urls.push(`  <url><loc>${HOST}${encodeURI(u)}</loc></url>`)
          }
        }
      }
      walk(outDir)
      writeFileSync(
        join(outDir, 'sitemap.xml'),
        `<?xml version="1.0" encoding="UTF-8"?>\n<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n${urls.join('\n')}\n</urlset>\n`,
      )
      // 空页即红：正文里的 {{ }} 会被 Vue 当插值整页吞掉而构建照常成功——
      // 这类静默失败必须有闸钉住。
      const empty: string[] = []
      const scan = (d: string) => {
        for (const e of readdirSync(d)) {
          const p = join(d, e)
          if (statSync(p).isDirectory()) scan(p)
          else if (e.endsWith('.html')) {
            const html = readFileSync(p, 'utf8')
            if (/class="vp-doc[^"]*"[^>]*><\/div>/.test(html)) empty.push(p)
          }
        }
      }
      scan(outDir)
      if (empty.length) {
        throw new Error('empty page bodies (Vue interpolation ate the content?): ' + empty.join(', '))
      }
    },
  }),
  { mermaid: {} },
)
