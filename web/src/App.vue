<script setup>
// SkinNexus · Copyright (C) 2026 SkinNexus contributors · AGPL-3.0
import { ref, computed, watch, onMounted, onBeforeUnmount, defineAsyncComponent, provide } from 'vue'
import { useI18n } from 'vue-i18n'
import { api, gateWatch } from './api.js'
import { transportError } from './errors.js'
import PageLoading from './components/PageLoading.vue'
import ToastHub from './components/ToastHub.vue'
import ModalHost from './components/ModalHost.vue'
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
const NotificationsPage = asyncPage(() => import('./pages/NotificationsPage.vue'))
const MyReportsPage = asyncPage(() => import('./pages/MyReportsPage.vue'))
const UnionBindPage = asyncPage(() => import('./pages/UnionBindPage.vue'))
const SetupPage = asyncPage(() => import('./pages/SetupPage.vue'))
const DashboardPage = asyncPage(() => import('./pages/DashboardPage.vue'))
const ClosetPage = asyncPage(() => import('./pages/ClosetPage.vue'))
const SkinlibPage = asyncPage(() => import('./pages/SkinlibPage.vue'))
const PlayersPage = asyncPage(() => import('./pages/PlayersPage.vue'))
const ProfilePage = asyncPage(() => import('./pages/ProfilePage.vue'))
const HelpPage = asyncPage(() => import('./pages/HelpPage.vue'))
const PluginPage = asyncPage(() => import('./components/plugin/PluginPage.vue'))
import { loadPluginUI, navPages, pageByPath, pluginLabel } from './pluginUI.js'
import { useSiteConfig } from './composables/useSiteConfig.js'
import { useSession } from './composables/useSession.js'
import { useGoto } from './composables/useGoto.js'
import { viewURLs, legacyRoutes } from './routes.js'
import LoginCard from './components/auth/LoginCard.vue'
import RegisterCard from './components/auth/RegisterCard.vue'
import ForgotCard from './components/auth/ForgotCard.vue'
import ResetCard from './components/auth/ResetCard.vue'
import VerifyCard from './components/auth/VerifyCard.vue'
import TopBar from './components/shell/TopBar.vue'
import UserSide from './components/shell/UserSide.vue'
import SiteFooter from './components/shell/SiteFooter.vue'
import { setLocale } from './i18n.js'
import { setUnauthorizedHandler } from './session.js'

const { t, locale } = useI18n()
// Site capabilities live in one composable so the auth cards and the shell read
// the same fetch result; App provides the ref to the tree under `siteConfig`.
const {
  siteConf, confLoaded, normalizeFooter,
  siteFooter, siteName, inviteFieldShown, inviteIsRequired,
  mailEnabled, verifyGateEnabled, emailFieldShown, unionEnabled,
} = useSiteConfig()

// Session state and the two sign-in guards live with the auth flow; App keeps
// the refs it renders and provides them to the tree under the frozen keys
// `sessionUser` and `authState`.
const {
  user, isAdmin, authErr, authBusy, locked, lockSecondsLeft, waitText, startWait,
  verifyPending, pendingBusy, pendingNote, pendingChangeOpen, pendingEmail,
  verifyMailTo, verifyMailBusy, verifyMailNote,
  refreshMe, pendingResend, pendingSaveEmail, resendVerificationMail, onInboxRead,
} = useSession()

// Which view the shell is on, and every transition into another one. The URL
// half stays below (viewURLs / legacyRoutes / the watches), because the source
// gates read the address-bar contract out of this file.
const {
  view, extPath, pendingExt, pendingView, menuOpen,
  openExt, finishAuth, authWaitingElsewhere, skinlibLogin, goHome, backToDashboard,
} = useGoto({ isAdmin, user })

// Frozen injection keys (W4-1): sessionUser, siteConfig, gotoView — and
// authState, which arrives with the auth-card components. These names are the
// shell's public API to every page and plugin frame; renaming one is a
// breaking change for code that lives outside this file, so each is declared
// here once and never repeated as a literal elsewhere.
provide('sessionUser', user)
// login | register | setup | device | dashboard | closet | players |
// profile | apps | union | admin
// Host-rendered plugin fields on the register card (ui.forms "auth.register").
const pluginFormData = ref({})


// State the SHELL owns because a route or another card writes it: the forgot
// card's sent-state (the login link clears it), and the reset token the mail
// brought in on the URL. Every field a card types into lives in that card.
const forgotSent = ref(false)
const resetToken = ref('')
// 邮箱验证 (2026-11-02): /email-verify?token=… 由邮件带入。落地只展示按钮、
// 点击才消费：邮件网关与微信/QQ 内置浏览器会预取链接，进页面即兑换会把
// 一次性令牌烧在玩家点击之前。
const verifyToken = ref('')
const verifyState = ref('')      // '' | 'ready' | 'working' | 'done' | 'invalid' | 'mailed'
// Public site capabilities (GET /api/v1/site-config): the auth forms follow
// the deployment's real configuration — invite field required-or-absent,
// email field + forgot flow available-or-hidden. Until the fetch resolves
// (or if it fails) the pre-config UI stays = today's behavior.
// Pages read the deployment's real limits off the same boot fetch instead of
// hardcoding them (the upload form states the byte ceiling it will enforce).
provide('siteConfig', siteConf)
// Pages hand the player to another page (an empty closet points at the players
// page) through the shell, instead of a full reload that drops the boot state.
provide('gotoView', (v) => { view.value = v })
// Boot gate: hide the auth/shell until session + site capabilities settle.
// Without it a logged-in refresh renders the login card first (the old boot
// awaited setup→config→pluginUI→me sequentially, so the flash lasted seconds
// behind a high-latency CDN).
const booting = ref(true)
const bootErr = ref('')

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
  notifications: '<path d="M18 8a6 6 0 1 0-12 0c0 7-3 9-3 9h18s-3-2-3-9M13.7 21a2 2 0 0 1-3.4 0"/>',
  help: '<circle cx="12" cy="12" r="9"/><path d="M9.5 9a2.5 2.5 0 1 1 3.5 2.3c-.7.4-1 .9-1 1.7v.5M12 17h.01"/>',
  myreports: '<path d="M5 21V4l14 3-3 5 3 5-14 3"/>',
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
      {
        id: 'notifications', labelKey: 'nav.notifications', icon: navIcons.notifications,
        badge: Number(user.value.unreadNotifications || 0),
      },
      { id: 'myreports', labelKey: 'nav.myReports', icon: navIcons.myreports },
      { id: 'apps', labelKey: 'nav.apps', icon: navIcons.apps },
      { id: 'help', labelKey: 'nav.help', icon: navIcons.help },
      ...(unionEnabled.value ? [{ id: 'union', labelKey: 'nav.union', icon: navIcons.union }] : []),
      ...pluginItems,
    ],
  }]
  if (isAdmin.value) groups.push({ items: [{ id: 'admin', labelKey: 'nav.adminPanel', icon: navIcons.admin }] })
  return groups
})


// Brand mark: the N plate (favicon.svg same artwork, brand colors) — the
// topbar/auth-scene signature. Logo colors are brand assets: never retheme.
const brandSVG = '<svg viewBox="0 0 512 512" xmlns="http://www.w3.org/2000/svg">'
  + '<defs><linearGradient id="bn-g" x1="0" y1="0" x2="1" y2="1">'
  + '<stop offset="0" stop-color="#2f66ff"/><stop offset="0.55" stop-color="#2a8fe0"/><stop offset="1" stop-color="#20b983"/>'
  + '</linearGradient></defs>'
  + '<rect x="36" y="36" width="440" height="440" rx="96" fill="#f4f8ff" stroke="#d3def8" stroke-width="2"/>'
  + '<path d="M150 372V150h52l108 138V150h52v222h-52L202 234v138z" fill="url(#bn-g)"/>'
  + '</svg>'


// ── URL ↔ 视图双向同步（2026-10-27 逻辑修复：地址栏与视图曾完全脱节，
//    同一皮肤库两个 URL 并存）──
// 两张映射表在 routes.js（数据），这里只有同步行为：/skinlib 是皮肤库
// 唯一规范地址，/user/skinlib 仅作 PCL/旧链接深链兼容。
let syncingURL = false
watch(view, (v) => {
  menuOpen.value = false
  let u = viewURLs[v]
  // The panel owns the segment below /admin (tab / plugin page). Entering the
  // panel surface must not clobber a deep link like /admin/users back to /admin
  // before AdminPanel mounts and reads it.
  if (v === 'admin' && (location.pathname === '/admin' || location.pathname.startsWith('/admin/'))) u = location.pathname
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
  } else if (location.pathname === '/email-verify') {
    view.value = 'verify'
    prepareEmailVerify()
  } else if (location.pathname === '/admin' || location.pathname.startsWith('/admin/')) {
    // AdminPanel's own popstate listener resolves the tab; here we only make
    // sure the shell is on the panel surface (or the login card when signed out).
    if (user.value) view.value = 'admin'
    else { pendingView.value = 'admin'; view.value = 'login' }
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

function withBootTimeout(promise, ms) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      // Translated like every other transport failure: this one used to put
      // the bare word "timeout" inside the boot error sentence.
      reject(new Error(transportError('networkTimeout')))
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
  // 邮箱验证: 落地只读 URL 展示按钮，预取不会烧掉一次性令牌（见 doEmailVerify）。
  else if (location.pathname === '/email-verify') {
    view.value = 'verify'
    prepareEmailVerify()
  }
  else if (!user.value && location.pathname === '/register') view.value = 'register'
  else if (location.pathname === '/admin' || location.pathname.startsWith('/admin/')) {
    // Panel deep link: logged in → straight to the panel (AdminPanel resolves
    // the tab/plugin from the URL); logged out → login card, resume after auth.
    if (user.value) view.value = 'admin'
    else pendingView.value = 'admin'
  }
  else if (legacyRoutes[location.pathname]) {
    // launcher deep link: logged in → straight there; logged out → login card,
    // resume into the target view after authentication. /skinlib and /help are
    // public: an anonymous visitor must be able to read them, not be bounced
    // into a login wall (help exists precisely for people stuck outside).
    if (user.value) view.value = legacyRoutes[location.pathname]
    else if (location.pathname === '/skinlib' || location.pathname === '/help') view.value = legacyRoutes[location.pathname]
    else pendingView.value = legacyRoutes[location.pathname]
  }
  else view.value = user.value ? 'dashboard' : 'login'
}

// A gate that closes mid-session must not leave a page full of buttons that
// 403: the transport reports the code, the shell re-reads its own state.
gateWatch.onVerifyRequired = refreshMe

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


// 邮箱验证 (2026-11-02): the link is single-use. Landing only reads the URL;
// the POST happens on click, because mail gateways and in-app browsers prefetch
// links and would spend the token before the player arrives. The server never
// says which check failed (unknown / expired / already used), so the UI has one
// failure state and it says exactly that.
function prepareEmailVerify() {
  verifyToken.value = new URLSearchParams(location.search).get('token') || ''
  verifyState.value = verifyToken.value ? 'ready' : 'invalid'
}

async function doEmailVerify() {
  if (verifyState.value === 'working' || verifyState.value === 'done') return
  if (!verifyToken.value) { verifyState.value = 'invalid'; return }
  verifyState.value = 'working'
  try {
    await api.confirmEmailVerification(verifyToken.value)
    verifyState.value = 'done'
  } catch (e) {
    verifyState.value = 'invalid'
  }
}

// The four sign-in cards share one state (only one is ever on screen), so it
// is provided as one object instead of four prop chains. This is the fourth
// frozen injection key; the cards inject it and never reach into this file.
provide('authState', {
  user, busy: authBusy, err: authErr, locked, waitText, startWait,
  finishAuth, authWaitingElsewhere,
  siteConf, confLoaded, mailEnabled, verifyGateEnabled, emailFieldShown,
  inviteFieldShown, inviteIsRequired,
  forgotSent, resetToken,
  verifyState, verifyMailTo, verifyMailBusy, verifyMailNote, verifyPending,
  prepareEmailVerify, doEmailVerify, resendVerificationMail,
  pluginFormData,
})

// Resend from the gated-signup landing. Same endpoint and same wording as the
// profile page's button; the server's own rate window answers, and its code
// arrives already run through the error dictionary.
// The three things a pending account may still do. Each one re-reads /me,
// because the verdict is the server's: after a resend, an address change or a
// successful exchange, the shell has to stop treating the player as blocked
// without a reload.
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
    <p class="error" role="alert">{{ t('common.dataLoadFailed', { message: bootErr }) }}</p>
    <button @click="boot">{{ t('common.retry') }}</button>
  </div>
  <div v-else class="app-shell">
    <a class="skip-link" href="#main-content">{{ t('common.skipToContent') }}</a>
    <!-- 顶栏：品牌区 + 语言 + 会话操作（sticky 玻璃带） -->
    <TopBar :view="view" :menu-open="menuOpen" :site-name="siteName"
            :brand-svg="brandSVG" @toggle-menu="menuOpen = !menuOpen" @logout="doLogout" />

    <div id="main-content" class="container" :class="{ wide: view === 'admin' }" tabindex="-1">
    <!-- AUTH VIEWS: 分栏场景式（左=远山+N 牌，右=表单）。独立 v-if：error 横幅
         不参与视图条件链 -->
    <template v-if="['login', 'register', 'forgot', 'reset', 'verify'].includes(view)">
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
    <LoginCard v-if="view === 'login'" />
    <RegisterCard v-else-if="view === 'register'" />
    <ForgotCard v-else-if="view === 'forgot'" />
    <ResetCard v-else-if="view === 'reset'" />
    <VerifyCard v-else-if="view === 'verify'" />
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

    <!-- help is public too — see applyInitialRoute -->
    <template v-else-if="!user && view === 'help'">
      <HelpPage />
    </template>

    <!-- The gate on the web side. Past the grace window an unverified account
         is not a member with a cookie — the server refuses everything that
         acts as this account — so the panel is replaced rather than annotated.
         Three ways out, all of them reachable: the link can be resent, the
         address can be corrected (a typo at signup is otherwise permanent),
         and the session can be ended. -->
    <template v-else-if="user && verifyPending">
      <div class="card verify-block">
        <h2>{{ t('auth.pendingTitle') }}</h2>
        <p class="muted" v-if="mailEnabled">{{ t('auth.pendingBody', { email: user.email || '' }) }}</p>
        <p class="error" role="alert" v-else>{{ t('auth.pendingNoMail') }}</p>
        <p class="muted" role="status" v-if="pendingNote">{{ pendingNote }}</p>
        <div class="row" style="margin-top: 16px; flex-wrap: wrap;">
          <button v-if="mailEnabled" :disabled="pendingBusy" @click="pendingResend">{{ t('auth.pendingResend') }}</button>
          <button class="secondary" @click="pendingChangeOpen = !pendingChangeOpen">{{ t('auth.pendingChange') }}</button>
          <button class="secondary" @click="doLogout">{{ t('common.logout') }}</button>
        </div>
        <div class="row" style="margin-top: 12px; flex-wrap: wrap;" v-if="pendingChangeOpen">
          <label class="fld" for="f-pending-email">{{ t('auth.pendingNewEmail') }}
            <input id="f-pending-email" v-model="pendingEmail" type="email" autocomplete="email" @keyup.enter="pendingSaveEmail" />
          </label>
          <button :disabled="pendingBusy" @click="pendingSaveEmail">{{ t('common.save') }}</button>
        </div>
      </div>
    </template>

    <!-- logged-in shell: sidebar + page (blessing 用户中心布局) -->
    <template v-else-if="user">
      <div class="user-shell">
        <UserSide v-if="view !== 'admin'" :view="view" :ext-path="extPath"
                  :nav-groups="navGroups" :user="user"
                  @open-ext="openExt" @logout="doLogout" />
        <main class="user-main">
          <DashboardPage v-if="view === 'dashboard'" />
          <SkinlibPage v-else-if="view === 'skinlib'" :user="user" @login="skinlibLogin" />
          <ClosetPage v-else-if="view === 'closet'" />
          <PlayersPage v-else-if="view === 'players'" />
          <ProfilePage v-else-if="view === 'profile'" :mail-enabled="mailEnabled" />
          <NotificationsPage v-else-if="view === 'notifications'" @read="onInboxRead" />
          <MyReportsPage v-else-if="view === 'myreports'" />
          <AuthorizedAppsPage v-else-if="view === 'apps'" />
          <HelpPage v-else-if="view === 'help'" />
          <UnionBindPage v-else-if="view === 'union'" />
          <PluginPage v-else-if="view === 'ext'" :path="extPath" />
          <AdminPanel v-else-if="view === 'admin'" :user="user" :site-conf="siteConf" :conf-loaded="confLoaded" />
        </main>
      </div>
    </template>
    </div><!-- /container -->
    <SiteFooter :site-footer="siteFooter" />
  </div><!-- /app-shell -->
  <ToastHub />
  <ModalHost />
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
