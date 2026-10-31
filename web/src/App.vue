<script setup>
// SkinNexus · Copyright (C) 2026 SkinNexus contributors · AGPL-3.0
import { ref, computed, watch, onMounted, defineAsyncComponent, provide } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from './api.js'
import PageLoading from './components/PageLoading.vue'
import ToastHub from './components/ToastHub.vue'
import PluginSlot from './components/plugin/PluginSlot.vue'
import PluginFields from './components/plugin/PluginFields.vue'
// Route-level chunks: the login card must not download the admin tables,
// Markdown pipeline or skinview3d-adjacent pages on a 3 Mbps link.
const asyncPage = (loader) => defineAsyncComponent({
  loader, loadingComponent: PageLoading, delay: 120,
})
const AdminPanel = asyncPage(() => import('./components/AdminPanel.vue'))
const DevicePage = asyncPage(() => import('./pages/DevicePage.vue'))
const AuthorizedAppsPage = asyncPage(() => import('./pages/AuthorizedAppsPage.vue'))
const UnionBindPage = asyncPage(() => import('./pages/UnionBindPage.vue'))
const SetupPage = asyncPage(() => import('./pages/SetupPage.vue'))
const DashboardPage = asyncPage(() => import('./pages/DashboardPage.vue'))
const ClosetPage = asyncPage(() => import('./pages/ClosetPage.vue'))
const SkinlibPage = asyncPage(() => import('./pages/SkinlibPage.vue'))
const PlayersPage = asyncPage(() => import('./pages/PlayersPage.vue'))
const ProfilePage = asyncPage(() => import('./pages/ProfilePage.vue'))
const PluginPage = asyncPage(() => import('./components/plugin/PluginPage.vue'))
import { loadPluginUI, navPages, pageByPath, pluginLabel } from './pluginUI.js'
import { setLocale } from './i18n.js'
import { setUnauthorizedHandler } from './session.js'

const { t, locale } = useI18n()

const user = ref(null)
// Admin panel entry (blessing parity): only accounts with permission > 0 see
// it — normal users never do. `permission` comes from GET /api/v1/me
// (0 = normal, >0 = admin, -1 = banned).
const isAdmin = computed(() => Number(user.value && user.value.permission) > 0)
provide('sessionUser', user)
// login | register | setup | device | dashboard | closet | players |
// profile | apps | union | admin
const view = ref('login')
const extPath = ref('')
const pendingExt = ref('')
// mobile topbar drawer (≤640px hamburger)
const menuOpen = ref(false)
const authErr = ref('') // login + register cards
// Host-rendered plugin fields on the register card (ui.forms "auth.register").
const pluginFormData = ref({})


// login form
const identifier = ref('')
const password = ref('')
const email = ref('')
const nickname = ref('')
const invitationCode = ref('')
// A1 找回密码: forgot card state + /reset-password?token=… reset card state
const forgotEmail = ref('')
const forgotSent = ref(false)
const resetToken = ref('')
const resetPw1 = ref('')
const resetPw2 = ref('')
const resetDone = ref(false)
// Public site capabilities (GET /api/v1/site-config): the auth forms follow
// the deployment's real configuration — invite field required-or-absent,
// email field + forgot flow available-or-hidden. Until the fetch resolves
// (or if it fails) the pre-config UI stays = today's behavior.
function defaultFooter() {
  return { text: '', links: [], siteName: 'SkinNexus', year: new Date().getFullYear() }
}
function normalizeFooter(raw) {
  const f = raw && typeof raw === 'object' ? raw : {}
  return { ...defaultFooter(), ...f, links: Array.isArray(f.links) ? f.links : [] }
}
const siteConf = ref({ site_name: '', invite_required: false, mail_configured: false, union_enabled: false, footer: defaultFooter() })
const siteFooter = computed(() => normalizeFooter(siteConf.value.footer))
const siteName = computed(() => siteConf.value.site_name || siteFooter.value.siteName || t('site.name'))
const confLoaded = ref(false)
// Boot gate: hide the auth/shell until session + site capabilities settle.
// Without it a logged-in refresh renders the login card first (the old boot
// awaited setup→config→pluginUI→me sequentially, so the flash lasted seconds
// behind a high-latency CDN).
const booting = ref(true)
const bootErr = ref('')
// Invite field is absent unless the server says it is required. The old
// "show an optional box while config loads" behavior produced a dead
// "邀请码（选填）" field on sites that never use invitations.
const inviteFieldShown = computed(() => confLoaded.value && siteConf.value.invite_required)
const inviteIsRequired = computed(() => confLoaded.value && siteConf.value.invite_required)
const mailEnabled = computed(() => confLoaded.value && siteConf.value.mail_configured)
const unionEnabled = computed(() => confLoaded.value && siteConf.value.union_enabled)

// Sidebar nav (blessing parity: 用户中心/管理 两组). device/apps/union are
// user-center entries too — kept out of the sidebar to preserve the
// launcher-OAuth flow's focused page (reachable from the dashboard copy).
// v3: nav items carry consistent line icons (1.7px stroke, currentColor).
const navIcons = {
  dashboard: '<path d="M3 3h7v7H3zM14 3h7v5h-7zM14 12h7v9h-7zM3 14h7v7H3z"/>',
  skinlib: '<path d="M4 4h7v7H4zM13 4h7v7h-7zM4 13h7v7H4zM13 13h7v7h-7z"/>',
  closet: '<path d="M12 3l9 5-9 5-9-5 9-5zM3 13l9 5 9-5M3 17l9 5 9-5"/>',
  players: '<path d="M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM4 21c0-4 3.6-6 8-6s8 2 8 6"/>',
  profile: '<path d="M3 5h18v14H3zM7 9h4M7 13h8M7 17h6"/>',
  apps: '<path d="M14 7a4 4 0 1 1 3 6.9L12 18l-3 3-2-2 3-3-4.1-5A4 4 0 0 1 14 7z"/>',
  union: '<path d="M12 3a9 9 0 1 0 9 9M12 3a9 9 0 0 1 9 9M12 8a4 4 0 1 0 4 4"/>',
  admin: '<path d="M12 3l7 3v6c0 4.5-3 7.5-7 9-4-1.5-7-4.5-7-9V6l7-3zM9.5 12l2 2 3.5-4"/>',
}
const navGroups = computed(() => {
  if (!user.value) return []
  const perm = Number(user.value.permission || 0)
  const pluginItems = navPages('user')
    .filter((p) => perm >= Number(p.perm || 0))
    .map((p) => ({ id: 'ext:' + p.path, label: pluginLabel(p.plugin, 'nav.' + p.id, p.nav.label), path: p.path, icon: navIcons.apps }))
  const groups = [{
    items: [
      { id: 'dashboard', labelKey: 'nav.dashboard', icon: navIcons.dashboard },
      { id: 'skinlib', labelKey: 'nav.skinlib', icon: navIcons.skinlib },
      { id: 'closet', labelKey: 'nav.closet', icon: navIcons.closet },
      { id: 'players', labelKey: 'nav.players', icon: navIcons.players },
      { id: 'profile', labelKey: 'nav.profile', icon: navIcons.profile },
      { id: 'apps', labelKey: 'nav.apps', icon: navIcons.apps },
      ...(unionEnabled.value ? [{ id: 'union', labelKey: 'nav.union', icon: navIcons.union }] : []),
      ...pluginItems,
    ],
  }]
  if (isAdmin.value) groups.push({ items: [{ id: 'admin', labelKey: 'nav.adminPanel', icon: navIcons.admin }] })
  return groups
})

function openExt(path) {
  const page = pageByPath(path)
  if (page && page.area === 'admin' && !isAdmin.value) {
    view.value = 'dashboard'
    return
  }
  extPath.value = path
  view.value = 'ext'
  if (location.pathname !== path) history.pushState({ ext: path }, '', path)
}

// Brand mark: the N plate (favicon.svg same artwork, brand colors) — the
// topbar/auth-scene signature. Logo colors are brand assets: never retheme.
const brandSVG = '<svg viewBox="0 0 512 512" xmlns="http://www.w3.org/2000/svg">'
  + '<defs><linearGradient id="bn-g" x1="0" y1="0" x2="1" y2="1">'
  + '<stop offset="0" stop-color="#2f66ff"/><stop offset="0.55" stop-color="#2a8fe0"/><stop offset="1" stop-color="#20b983"/>'
  + '</linearGradient></defs>'
  + '<rect x="36" y="36" width="440" height="440" rx="96" fill="#f4f8ff" stroke="#d3def8" stroke-width="2"/>'
  + '<path d="M150 372V150h52l108 138V150h52v222h-52L202 234v138z" fill="url(#bn-g)"/>'
  + '</svg>'

function goHome() {
  // 品牌点击 = 回工作台（URL 由下方 watch 同步为 /user/dashboard，
  // 不再 replaceState '/' —— 那曾造成地址栏与视图脱节）
  view.value = user.value ? 'dashboard' : 'login'
}

// ── URL ↔ 视图双向同步（2026-10-27 逻辑修复：地址栏与视图曾完全脱节，
//    同一皮肤库两个 URL 并存）──
// /skinlib 为皮肤库唯一规范地址；/user/skinlib 仅作 PCL/旧链接深链兼容。
const viewURLs = {
  skinlib: '/skinlib',
  closet: '/user/closet',
  profile: '/user/profile',
  players: '/user/players',
  dashboard: '/user/dashboard',
}
let syncingURL = false
watch(view, (v) => {
  menuOpen.value = false
  const u = viewURLs[v]
  if (u && location.pathname !== u && !syncingURL) history.pushState({ view: v }, '', u)
})
// Union is a hot module: if it is disabled while the bind page is open,
// leave the now-hidden page instead of showing a dead surface.
watch(unionEnabled, (on) => {
  if (!on && view.value === 'union') view.value = 'dashboard'
})
function onPopState() {
  syncingURL = true
  if (location.pathname.startsWith('/ext/')) {
    if (user.value) openExt(location.pathname)
    else { pendingExt.value = location.pathname; view.value = 'login' }
  } else {
    const v = legacyRoutes[location.pathname]
    if (v) view.value = v
    else if (location.pathname === '/') view.value = user.value ? 'dashboard' : 'login'
  }
  syncingURL = false
}

// Session-drop handler: pages call notifyUnauthorized() on 401 → back to login.
setUnauthorizedHandler(() => { user.value = null; view.value = 'login' })

// Launcher deep links (blessing route parity): PCL derives the skin-management
// and profile pages by swapping the API root's yggdrasil path for
// `user/closet` / `user/profile` (PageLoginProfileSkin.xaml.cs:151,
// ProfileUi.cs:291) — the SPA must honor those URLs or the launcher's "换皮肤 /
// 修改密码" buttons land on the dashboard. Same for HMCL's yggdrasil convention.
const legacyRoutes = {
  '/user/closet': 'closet',
  '/user/profile': 'profile',
  '/user/players': 'players',
  '/user/skinlib': 'skinlib',
  '/user/dashboard': 'dashboard',
  '/skinlib': 'skinlib',
}
// Deep-linked view held for after login (open /user/closet logged-out → login
// card first, then drop straight into the closet).
const pendingView = ref('')

function withBootTimeout(promise, ms) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      const err = new Error('timeout')
      err.code = 'timeout'
      reject(err)
    }, ms)
    promise.then((v) => { clearTimeout(timer); resolve(v) },
      (e) => { clearTimeout(timer); reject(e) })
  })
}

function applyInitialRoute() {
  const isDevice = location.pathname === '/device'
  if (location.pathname.startsWith('/ext/')) {
    if (user.value) openExt(location.pathname)
    else { pendingExt.value = location.pathname; view.value = 'login' }
  }
  else if (isDevice) view.value = 'device'
  // A1 找回密码: /reset-password?token=… comes back from the mail as a real
  // URL; /forgot-password is the shareable form of the login-card link.
  else if (location.pathname === '/reset-password') {
    resetToken.value = new URLSearchParams(location.search).get('token') || ''
    view.value = 'reset'
  }
  else if (location.pathname === '/forgot-password') view.value = 'forgot'
  else if (!user.value && location.pathname === '/register') view.value = 'register'
  else if (legacyRoutes[location.pathname]) {
    // launcher deep link: logged in → straight there; logged out → login card,
    // resume into the target view after authentication. /skinlib is public.
    if (user.value) view.value = legacyRoutes[location.pathname]
    else if (location.pathname === '/skinlib') view.value = 'skinlib'
    else pendingView.value = legacyRoutes[location.pathname]
  }
  else view.value = user.value ? 'dashboard' : 'login'
}

async function boot() {
  booting.value = true
  bootErr.value = ''
  // One parallel round trip instead of four sequential ones: the login card
  // must not wait for setup→config→pluginUI before `me` even starts.
  const [setupR, confR, , meR] = await Promise.allSettled([
    withBootTimeout(fetch('/api/v1/setup/status').then((r) => r.json()), 8000),
    withBootTimeout(api.siteConfig(), 8000),
    loadPluginUI(),
    withBootTimeout(api.me(), 10000),
  ])
  if (setupR.status === 'fulfilled' && setupR.value && setupR.value.needs_setup) {
    view.value = 'setup'
    booting.value = false
    return
  }
  if (confR.status === 'fulfilled') {
    const conf = confR.value || {}
    siteConf.value = { ...siteConf.value, ...conf, footer: normalizeFooter(conf.footer) }
    confLoaded.value = true
  }
  if (meR.status === 'fulfilled') {
    user.value = meR.value || null
  } else if (meR.reason && meR.reason.status === 401) {
    // A real "no session" answer — fall through to the login card.
    user.value = null
  } else {
    // Network/5xx/timeout: a transient failure is NOT a logout. Showing the
    // login card here made flaky high-latency links look like session loss.
    bootErr.value = (meR.reason && meR.reason.message) || 'network error'
    booting.value = false
    return
  }
  applyInitialRoute()
  booting.value = false
}

function backToDashboard() {
  // restore the site root URL when leaving the device page
  if (location.pathname === '/device') history.replaceState(null, '', '/')
  view.value = 'dashboard'
}

function onLocaleChange(e) {
  setLocale(e.target.value)
}

// 登录/注册共同收尾：?next= 回跳（详情页 viewer 401 引导来的）优先整页跳回；
// 其次设备页流程、launcher 深链恢复；落地即清 pendingView（防"迟到恢复"跳页）。
function finishAuth() {
  const next = new URLSearchParams(location.search).get('next')
  if (next && next.startsWith('/') && !next.startsWith('//')) {
    pendingView.value = ''
    location.href = next
    return
  }
  if (location.pathname === '/device') view.value = 'device'
  else if (pendingExt.value) { const p = pendingExt.value; pendingExt.value = ''; openExt(p) }
  else if (pendingView.value) { view.value = pendingView.value; pendingView.value = '' }
  else view.value = 'dashboard'
}

// 皮肤库访客点 ♥ 等需登录操作：登录后回到皮肤库（pendingView 闭环）
function skinlibLogin() { pendingView.value = 'skinlib'; view.value = 'login' }

async function doLogin() {
  authErr.value = ''
  try {
    user.value = await api.login(identifier.value, password.value)
    // New backends return `permission` on login; fall back to /me only for
    // older servers (saves one RTT on every login).
    if (user.value && user.value.permission === undefined) {
      try { user.value = await api.me() } catch { /* keep login payload */ }
    }
  } catch (e) { authErr.value = e.message; return }
  finishAuth()
}

async function doRegister() {
  authErr.value = ''
  // Invite required → fail fast client-side with a clear message instead of
  // letting the server 400 the whole submission.
  if (inviteIsRequired.value && !invitationCode.value.trim()) {
    authErr.value = t('auth.inviteRequiredErr'); return
  }
  try {
    user.value = await api.register(email.value, nickname.value, password.value, invitationCode.value.trim(), pluginFormData.value)
    if (user.value && user.value.permission === undefined) user.value = { ...user.value, permission: 0 }
  } catch (e) { authErr.value = e.message; return }
  finishAuth()
}

// A1 找回密码: forgot always shows the same sent-state (server is
// enumeration-safe); reset validates the two fields client-side first.
async function doForgot() {
  authErr.value = ''
  try {
    await api.forgotPassword(forgotEmail.value.trim())
    forgotSent.value = true
  } catch (e) { authErr.value = e.message }
}

async function doReset() {
  authErr.value = ''
  if (resetPw1.value !== resetPw2.value) { authErr.value = t('auth.resetMismatch'); return }
  try {
    await api.resetPassword(resetToken.value, resetPw1.value)
    resetDone.value = true
  } catch (e) { authErr.value = e.message }
}

async function doLogout() {
  try {
    await api.logout()
  } catch { /* session may already be gone — still sign out locally */ }
  user.value = null
  extPath.value = ''
  view.value = 'login'
}

onMounted(() => {
  boot()
  window.addEventListener('popstate', onPopState)
  window.addEventListener('keydown', (e) => { if (e.key === 'Escape') menuOpen.value = false })
})
</script>

<template>
  <div v-if="booting" class="boot-screen" role="status" aria-live="polite">
    <span class="boot-logo" v-html="brandSVG"></span>
    <span class="spinner boot-spinner" aria-hidden="true"></span>
    <p class="muted">{{ t('common.loading') }}</p>
  </div>
  <div v-else-if="bootErr" class="boot-screen">
    <p class="error">{{ t('common.dataLoadFailed', { message: bootErr }) }}</p>
    <button @click="boot">{{ t('common.retry') }}</button>
  </div>
  <div v-else class="app-shell">
    <!-- 顶栏：品牌区 + 语言 + 会话操作（sticky 玻璃带） -->
    <header class="topbar" :class="{ wide: view === 'admin' }">
      <div class="topbar-inner">
        <a class="brand" href="/" @click.prevent="goHome" :title="t('site.tagline')">
          <span class="brand-n" v-html="brandSVG"></span>
          <span class="brand-name">{{ siteName }}</span>
        </a>
        <div class="topbar-right" :class="{ open: menuOpen }">
          <select :value="locale" class="locale-sel" @change="onLocaleChange">
            <option value="zh-CN">中文</option>
            <option value="en">English</option>
          </select>
          <button v-if="view === 'admin'" class="secondary" @click="view = 'dashboard'">← {{ t('nav.backToDashboard') }}</button>
          <template v-if="user">
            <span class="badge">{{ user.nickname }}</span>
            <button class="secondary" @click="doLogout">{{ t('common.logout') }}</button>
          </template>
          <template v-else>
            <button class="secondary" @click="view = 'skinlib'">{{ t('nav.skinlib') }}</button>
            <button class="secondary" @click="view = 'login'">{{ t('common.login') }}</button>
            <button @click="view = 'register'">{{ t('common.register') }}</button>
          </template>
          <PluginSlot slot="app.topbar.after" />
        </div>
        <button class="secondary nav-toggle" :aria-expanded="menuOpen ? 'true' : 'false'"
                :aria-label="menuOpen ? t('common.close') : t('nav.menu')" @click="menuOpen = !menuOpen">
          <svg v-if="!menuOpen" class="nav-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor"
               stroke-width="1.7" stroke-linecap="round" aria-hidden="true"><path d="M4 6h16M4 12h16M4 18h16" /></svg>
          <svg v-else class="nav-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor"
               stroke-width="1.7" stroke-linecap="round" aria-hidden="true"><path d="M5 5l14 14M19 5L5 19" /></svg>
        </button>
      </div>
    </header>

    <div class="container" :class="{ wide: view === 'admin' }">
    <!-- AUTH VIEWS: 分栏场景式（左=远山+N 牌，右=表单）。独立 v-if：error 横幅
         不参与视图条件链 -->
    <template v-if="['login', 'register', 'forgot', 'reset'].includes(view)">
      <div class="auth-split">
        <aside class="auth-scene" aria-hidden="true">
          <span class="brandmark" v-html="brandSVG"></span>
          <svg class="scene-mountains" xmlns="http://www.w3.org/2000/svg" viewBox="0 0 600 320" preserveAspectRatio="none">
            <path class="mtn-far" d="M0,200 L80,168 170,192 260,140 350,178 440,132 520,170 600,150 V320 H0 Z"/>
            <path class="mtn-mid" d="M0,242 L110,212 230,238 340,198 450,234 540,204 600,226 V320 H0 Z"/>
            <path class="mtn-near" d="M0,282 L130,260 280,278 430,254 600,272 V320 H0 Z"/>
          </svg>
          <div class="scene-fog fog-a"></div>
          <div class="scene-fog fog-b"></div>
          <div class="scene-copy">
            <h1>{{ siteName }}</h1>
            <div class="scene-fact">{{ t('site.tagline') }}</div>
          </div>
        </aside>
        <div class="auth-pane">

    <!-- LOGIN -->
    <div v-if="view === 'login'" class="card">
      <h2>{{ t('auth.loginTitle') }}</h2>
      <label>{{ t('auth.identifier') }}</label>
      <input v-model="identifier" autocomplete="username" />
      <label>{{ t('auth.password') }}</label>
      <input v-model="password" type="password" autocomplete="current-password" @keyup.enter="doLogin" />
      <div style="margin-top: 8px; text-align: right;" v-if="mailEnabled">
        <a href="/forgot-password" class="muted" @click.prevent="forgotSent = false; view = 'forgot'">{{ t('auth.forgotLink') }}</a>
      </div>
      <div class="error" v-if="authErr">{{ authErr }}</div>
      <div class="row" style="margin-top: 16px;">
        <button @click="doLogin">{{ t('common.login') }}</button>
        <button class="secondary" @click="view = 'register'">{{ t('common.register') }}</button>
      </div>
      <PluginSlot slot="auth.login.after" />
    </div>

    <!-- REGISTER -->
    <div v-else-if="view === 'register'" class="card">
      <h2>{{ t('auth.registerTitle') }}</h2>
      <label>{{ t('auth.nickname') }}</label>
      <input v-model="nickname" />
      <template v-if="mailEnabled">
        <label>{{ t('auth.email') }}</label>
        <input v-model="email" type="email" />
      </template>
      <label>{{ t('auth.passwordMin') }}</label>
      <input v-model="password" type="password" @keyup.enter="doRegister" />
      <template v-if="inviteFieldShown">
        <label class="required">{{ t('auth.invitationCodeRequired') }}</label>
        <input v-model="invitationCode" @keyup.enter="doRegister" />
      </template>
      <PluginFields form="auth.register" v-model="pluginFormData" />
      <div class="error" v-if="authErr">{{ authErr }}</div>
      <div class="row" style="margin-top: 16px;">
        <button @click="doRegister">{{ t('common.register') }}</button>
        <button class="secondary" @click="view = 'login'">{{ t('auth.toLogin') }}</button>
      </div>
      <PluginSlot slot="auth.register.after" />
    </div>

    <!-- FORGOT PASSWORD (A1 找回密码) -->
    <div v-else-if="view === 'forgot'" class="card">
      <h2>{{ t('auth.forgotTitle') }}</h2>
      <!-- mail unconfigured → the flow is honestly unavailable (direct
           navigation to /forgot-password lands here too) -->
      <template v-if="confLoaded && !siteConf.mail_configured">
        <p class="muted">{{ t('auth.mailDisabled') }}</p>
        <div class="row" style="margin-top: 16px;">
          <button class="secondary" @click="view = 'login'">{{ t('auth.toLogin') }}</button>
        </div>
      </template>
      <template v-else-if="!forgotSent">
        <label>{{ t('auth.forgotEmail') }}</label>
        <input v-model="forgotEmail" type="email" autocomplete="email" @keyup.enter="doForgot" />
        <div class="error" v-if="authErr">{{ authErr }}</div>
        <div class="row" style="margin-top: 16px;">
          <button @click="doForgot">{{ t('auth.forgotSubmit') }}</button>
          <button class="secondary" @click="view = 'login'">{{ t('auth.toLogin') }}</button>
        </div>
      </template>
      <template v-else>
        <p class="muted">{{ t('auth.forgotSent') }}</p>
        <div class="row" style="margin-top: 16px;">
          <button class="secondary" @click="view = 'login'">{{ t('auth.toLogin') }}</button>
        </div>
      </template>
    </div>

    <!-- RESET PASSWORD (A1, /reset-password?token=…) -->
    <div v-else-if="view === 'reset'" class="card">
      <h2>{{ t('auth.resetTitle') }}</h2>
      <template v-if="resetDone">
        <p class="muted">{{ t('auth.resetDone') }}</p>
        <div class="row" style="margin-top: 16px;">
          <button @click="view = 'login'">{{ t('common.login') }}</button>
        </div>
      </template>
      <template v-else-if="resetToken">
        <label>{{ t('auth.resetNew') }}</label>
        <input v-model="resetPw1" type="password" autocomplete="new-password" />
        <label>{{ t('auth.resetConfirm') }}</label>
        <input v-model="resetPw2" type="password" autocomplete="new-password" @keyup.enter="doReset" />
        <div class="error" v-if="authErr">{{ authErr }}</div>
        <div class="row" style="margin-top: 16px;">
          <button @click="doReset">{{ t('auth.resetSubmit') }}</button>
          <button class="secondary" @click="view = 'login'">{{ t('auth.toLogin') }}</button>
        </div>
      </template>
      <template v-else>
        <p class="error">{{ t('auth.resetInvalid') }}</p>
        <div class="row" style="margin-top: 16px;">
          <button v-if="mailEnabled" @click="forgotSent = false; view = 'forgot'">{{ t('auth.forgotSubmit') }}</button>
          <button class="secondary" @click="view = 'login'">{{ t('auth.toLogin') }}</button>
        </div>
      </template>
    </div>
        </div><!-- /auth-pane -->
      </div><!-- /auth-split -->
    </template>

    <!-- SETUP wizard (first run) -->
    <template v-else-if="view === 'setup'">
      <SetupPage />
    </template>

    <!-- DEVICE verification (launcher OAuth) -->
    <template v-else-if="view === 'device'">
      <div v-if="!user" class="card" style="max-width: 420px;">
        <h2>{{ t('device.needLogin') }}</h2>
        <div class="muted">{{ t('device.needLoginHint') }}</div>
        <button @click="view = 'login'">{{ t('device.goLogin') }}</button>
      </div>
      <template v-else>
        <DevicePage />
        <div class="row" style="margin-top: 12px;">
          <button class="secondary" @click="backToDashboard">{{ t('nav.backToDashboard') }}</button>
        </div>
      </template>
    </template>

    <!-- public skin library: anonymous visitors browse without a session -->
    <template v-else-if="!user && view === 'skinlib'">
      <SkinlibPage :user="user" @login="skinlibLogin" />
    </template>

    <!-- logged-in shell: sidebar + page (blessing 用户中心布局) -->
    <template v-else-if="user">
      <div class="user-shell">
        <aside v-if="view !== 'admin'" class="card user-side">
          <nav v-for="(g, gi) in navGroups" :key="gi" class="nav-group">
            <div class="nav-title" v-if="g.items.length">{{ gi === 0 ? t('nav.userCenter') : t('nav.manageGroup') }}</div>
            <button v-for="n in g.items" :key="n.id" class="nav-item"
                    :class="{ active: n.path ? (view === 'ext' && extPath === n.path) : view === n.id }"
                    @click="n.path ? openExt(n.path) : (view = n.id)">
              <svg class="nav-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor"
                   stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" v-html="n.icon"></svg>
              <span>{{ n.label || t(n.labelKey) }}</span>
            </button>
          </nav>
          <!-- 底部用户卡：昵称 + 退出（v3 布局） -->
          <div class="user-chip">
            <span class="who" :title="user.nickname">{{ user.nickname }}</span>
            <button class="secondary" @click="doLogout">{{ t('common.logout') }}</button>
          </div>
        </aside>
        <main class="user-main">
          <DashboardPage v-if="view === 'dashboard'" />
          <SkinlibPage v-else-if="view === 'skinlib'" :user="user" @login="skinlibLogin" />
          <ClosetPage v-else-if="view === 'closet'" />
          <PlayersPage v-else-if="view === 'players'" />
          <ProfilePage v-else-if="view === 'profile'" />
          <AuthorizedAppsPage v-else-if="view === 'apps'" />
          <UnionBindPage v-else-if="view === 'union'" />
          <PluginPage v-else-if="view === 'ext'" :path="extPath" />
          <AdminPanel v-else-if="view === 'admin'" :user="user" :site-conf="siteConf" :conf-loaded="confLoaded" />
        </main>
      </div>
    </template>
    </div><!-- /container -->
    <PluginSlot slot="footer.before" />
    <!-- SkinNexus · Copyright (C) 2026 SkinNexus contributors · AGPL-3.0 -->
    <footer class="site-footer">
      <nav v-if="siteFooter.links.length" class="site-footer-links" aria-label="footer links">
        <a v-for="(l, i) in siteFooter.links" :key="i" :href="l.url" target="_blank" rel="noopener noreferrer">{{ l.label }}</a>
      </nav>
      <p v-if="siteFooter.text" class="site-footer-text">{{ siteFooter.text }}</p>
      <p class="site-footer-credit">
        Powered by <a href="https://github.com/YimingZhanshen/skin-nexus" target="_blank" rel="noopener noreferrer">SkinNexus</a><template v-if="siteFooter.siteName"> · © {{ siteFooter.year }} {{ siteFooter.siteName }}</template>
      </p>
    </footer>
  </div><!-- /app-shell -->
  <ToastHub />
</template>

<style scoped>
/* v3: shell 全局样式统一在 style.css（topbar/auth-split/侧栏图标/用户卡），
   scoped 不再重复定义 —— 主题包按全局类覆盖。 */
.boot-screen {
  min-height: 100dvh;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 14px;
  padding: 24px;
  text-align: center;
  background: var(--bg, #101825);
  color: var(--text, #e9edf1);
}
.boot-logo { width: 56px; height: 56px; display: block; }
.boot-logo :deep(svg) { width: 100%; height: 100%; }
.boot-spinner { width: 22px; height: 22px; }
</style>
