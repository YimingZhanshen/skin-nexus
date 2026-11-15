import { defineConfig } from 'vitepress'
import { withMermaid } from 'vitepress-plugin-mermaid'
import { chineseSearchOptimize, pagefindPlugin } from 'vitepress-plugin-pagefind'
import llmstxt from 'vitepress-plugin-llms'

// 文档站配置：docs.skin.yimingzhanshen.cn（Cloudflare Pages 托管，CF 管 TLS）。
// 内容 = 契约主目录（deploy/migration/build/spec…）+ handbook/ 知识手册（80 篇）。
// SEO 是本站主因：静态渲染 + sitemap + llms.txt（AI 答案引擎入口）。
const HOST = 'https://docs.skin.yimingzhanshen.cn'

export default withMermaid(
  defineConfig({
    lang: 'zh-CN',
    title: 'SkinNexus 文档',
    description:
      'Self-hosted Minecraft skin server documentation — deploy a Go skin server with Yggdrasil external auth and migrate losslessly from blessing-skin-server.',
    base: '/',
    cleanUrls: true,
    head: [
      ['link', { rel: 'sitemap', type: 'application/xml', href: '/sitemap.xml' }],
    ],
    markdown: {
      lineNumbers: false,
    },
    themeConfig: {
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
      const { readdirSync, statSync, writeFileSync } = await import('node:fs')
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
    },
  }),
  { mermaid: {} },
)
