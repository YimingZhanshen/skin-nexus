<script setup>
// AdminPanel shell (B1 refactor, 2026-10-24): sidebar + per-domain views.
// The old single-file card flow moved to components/admin/*; App.vue still
// mounts this at view === 'admin' (routing untouched).
import { ref, shallowRef, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../api.js'
import { token, needToken, persistToken } from '../adminApi.js'
import DashboardView from './admin/DashboardView.vue'
import UsersView from './admin/UsersView.vue'
import PlayersView from './admin/PlayersView.vue'
import InvitationView from './admin/InvitationView.vue'
import SettingsView from './admin/SettingsView.vue'
import StatusView from './admin/StatusView.vue'
import UnionView from './admin/UnionView.vue'
import AccessLogsView from './admin/AccessLogsView.vue'
import ReportsView from './admin/ReportsView.vue'
import TexturesView from './admin/TexturesView.vue'
import ClosetView from './admin/ClosetView.vue'
import AuditView from './admin/AuditView.vue'
import JobsView from './admin/JobsView.vue'
import PluginsView from './admin/PluginsView.vue'
import MarketView from './admin/MarketView.vue'
import PluginPage from './plugin/PluginPage.vue'
import { navPages, pluginLabel } from '../pluginUI.js'

const { t } = useI18n()

// Sidebar navigation (blessing 管理面板 order). PlayersView is the full
// management table (B3 landed). Icons mirror the user-sidebar line set.
const adminIcons = {
  dashboard: '<path d="M3 3h7v7H3zM14 3h7v5h-7zM14 12h7v9h-7zM3 14h7v7H3z"/>',
  users: '<path d="M9 11a3.5 3.5 0 1 0 0-7 3.5 3.5 0 0 0 0 7zM2 21c0-3.5 3.1-5.5 7-5.5s7 2 7 5.5"/><path d="M17 4.5a3 3 0 0 1 0 5.7M18 15c2.4.6 4 2.3 4 4.5"/>',
  players: '<path d="M12 12a4 4 0 1 0 0-8 4 4 0 0 0 0 8zM4 21c0-4 3.6-6 8-6s8 2 8 6"/>',
  invitation: '<path d="M3 6h18v4a2 2 0 0 0 0 4v4H3v-4a2 2 0 0 0 0-4z"/><path d="M13 8v2M13 14v2"/>',
  settings: '<path d="M4 6h16M4 12h16M4 18h16"/><circle cx="8" cy="6" r="2"/><circle cx="16" cy="12" r="2"/><circle cx="10" cy="18" r="2"/>',
  status: '<path d="M3 12h4l2-7 4 14 2-7h6"/>',
  access: '<path d="M6 3h9l4 4v14H6zM15 3v5h5"/><path d="M9 12h7M9 16h7"/>',
  reports: '<path d="M5 21V4l14 3-3 5 3 5-14 3"/>',
  union: '<path d="M12 3a9 9 0 1 0 9 9M12 3a9 9 0 0 1 9 9M12 8a4 4 0 1 0 4 4"/>',
  plugins: '<path d="M4 8h4a2 2 0 1 0 4 0h4v4a2 2 0 1 0 0 4v4h-4a2 2 0 1 1-4 0H4v-4a2 2 0 1 1 0-4z"/>',
  market: '<path d="M3 9l2-5h14l2 5M4 9h16v11H4zM9 13h6"/>',
  textures: '<path d="M9 4l3 2 3-2 4 3-2 3v9H7v-9L5 7z"/>',
  closet: '<path d="M12 3l9 5-9 5-9-5 9-5zM3 13l9 5 9-5M3 17l9 5 9-5"/>',
  audit: '<path d="M6 3h12v18H6z"/><path d="M9 8h6M9 12h6M9 16h4"/>',
  jobs: '<path d="M4 6h10M4 12h10M4 18h7"/><circle cx="18" cy="16" r="4"/><path d="M18 14.2v2l1.6 1.4"/>',
}
const nav = [
  { id: 'dashboard', labelKey: 'admin.navDashboard', comp: DashboardView, icon: adminIcons.dashboard },
  { id: 'users', labelKey: 'admin.navUsers', comp: UsersView, icon: adminIcons.users },
  { id: 'players', labelKey: 'admin.navPlayers', comp: PlayersView, icon: adminIcons.players },
  { id: 'invitation', labelKey: 'admin.navInvitation', comp: InvitationView, icon: adminIcons.invitation },
  { id: 'settings', labelKey: 'admin.navSettings', comp: SettingsView, icon: adminIcons.settings },
  { id: 'status', labelKey: 'admin.navStatus', comp: StatusView, icon: adminIcons.status },
  { id: 'access', labelKey: 'admin.navAccess', comp: AccessLogsView, icon: adminIcons.access },
  { id: 'reports', labelKey: 'admin.navReports', comp: ReportsView, icon: adminIcons.reports },
  { id: 'textures', labelKey: 'admin.navTextures', comp: TexturesView, icon: adminIcons.textures },
  { id: 'closet', labelKey: 'admin.navCloset', comp: ClosetView, icon: adminIcons.closet },
  { id: 'audit', labelKey: 'admin.navAudit', comp: AuditView, icon: adminIcons.audit },
  { id: 'jobs', labelKey: 'admin.navJobs', comp: JobsView, icon: adminIcons.jobs },
  { id: 'union', labelKey: 'admin.navUnion', comp: UnionView, icon: adminIcons.union },
  { id: 'plugins', labelKey: 'admin.navPlugins', comp: PluginsView, icon: adminIcons.plugins },
  { id: 'market', labelKey: 'admin.navMarket', comp: MarketView, icon: adminIcons.market },
]
// App boot already resolved the session, site capabilities and plugin UI;
// re-fetching all three here cost three serial RTTs behind a slow edge.
const props = defineProps({
  user: { type: Object, default: null },
  siteConf: { type: Object, default: () => ({}) },
  confLoaded: { type: Boolean, default: false },
})
const current = ref('dashboard')
const menuOpen = ref(false)
const currentLabel = computed(() => {
  const hit = nav.find((n) => n.id === current.value)
  return hit ? t(hit.labelKey) : t('admin.title')
})
const currentComp = shallowRef(DashboardView)
const currentPlugin = ref('')
const pluginPages = ref([])
const myPerm = ref(Number(props.user && props.user.permission) || 0)
// Keep the initial sidebar stable: use the already-loaded site config, and
// hide module entries while the state is unknown instead of adding/removing
// them after the page has painted.
const flags = ref({
  invite_required: props.confLoaded ? !!props.siteConf.invite_required : false,
  union_enabled: props.confLoaded ? !!props.siteConf.union_enabled : false,
})
const visibleNav = computed(() => nav.filter((n) =>
  (n.id !== 'invitation' || flags.value.invite_required) &&
  (n.id !== 'union' || flags.value.union_enabled)
))

async function loadFlags() {
  if (props.confLoaded) {
    flags.value = { ...flags.value, ...props.siteConf }
    return
  }
  try { flags.value = { ...flags.value, ...(await api.siteConfig()) } } catch { /* keep defaults */ }
}

function onSettingsChanged(key, value) {
  if (key === 'invite_required') flags.value.invite_required = value === '1'
  if (key === 'union_enabled') flags.value.union_enabled = value === '1'
  if (!visibleNav.value.some((n) => n.id === current.value)) go('dashboard')
}

function go(id) {
  const item = nav.find((n) => n.id === id)
  if (item) {
    currentPlugin.value = ''
    current.value = id
    currentComp.value = item.comp
  }
}

function openPlugin(path) {
  currentPlugin.value = path
  current.value = ''
}

// Deep-link: the active tab / plugin page lives in the URL (/admin/<tab>,
// /admin/plugin/<id>) so refresh, share and back/forward keep the view instead
// of dropping to the panel dashboard. App.vue owns the /admin surface; this
// component owns the segment below it and mirrors it into history.
let syncingURL = false
function adminURL() {
  if (currentPlugin.value) return '/admin/plugin/' + currentPlugin.value
  if (current.value && current.value !== 'dashboard') return '/admin/' + current.value
  return '/admin'
}
function applyAdminPath(p) {
  if (p !== '/admin' && !p.startsWith('/admin/')) return
  const rest = p.slice('/admin'.length).replace(/^\//, '')
  if (rest.startsWith('plugin/')) {
    currentPlugin.value = rest.slice('plugin/'.length)
    current.value = ''
    return
  }
  const item = nav.find((n) => n.id === rest)
  currentPlugin.value = ''
  current.value = item ? item.id : 'dashboard'
  currentComp.value = item ? item.comp : DashboardView
}
function onAdminPop() {
  syncingURL = true
  applyAdminPath(location.pathname)
  syncingURL = false
}
watch([current, currentPlugin], () => {
  if (syncingURL) return
  const u = adminURL()
  if (location.pathname !== u) history.pushState({ admin: u }, '', u)
})
onMounted(() => {
  syncingURL = true
  applyAdminPath(location.pathname)
  syncingURL = false
  window.addEventListener('popstate', onAdminPop)
})
onBeforeUnmount(() => window.removeEventListener('popstate', onAdminPop))

// The load button re-runs the current view's data fetch: views fetch their
// own data on mount, so remounting via a key bump is the reload contract.
const reloadTick = ref(0)
// Views mount (and fetch) only once admin access is established — a session
// with permission > 0 or a persisted/entered token. Otherwise the shell
// shows just the sidebar + token card (no 403 spam from every view).
const ready = ref(false)

async function submitToken() {
  persistToken()
  if (token.value) ready.value = true
  reloadTick.value++
}

// A session admin who once pasted the machine token keeps sending it on every
// request. The server now resolves the human first, but the secret is still
// sitting in this browser, so let them see it and drop it.
function clearStoredToken() {
  token.value = ''
  persistToken()
  reloadTick.value++
}

onMounted(async () => {
  // Session-admin detection: permission > 0 means the session alone reaches
  // admin data — no token ceremony needed.
  if (myPerm.value > 0) needToken.value = false
  pluginPages.value = navPages('admin')
    .filter((p) => myPerm.value >= Number(p.perm || 0))
    .map((p) => ({ ...p, icon: adminIcons.plugins }))
  await loadFlags()
  if (!needToken.value || token.value) {
    ready.value = true
    reloadTick.value++
  }
})
</script>

<template>
  <div class="admin-shell">
    <aside class="card admin-side" :class="{ open: menuOpen }">
      <button class="secondary nav-toggle side-toggle" :aria-expanded="menuOpen ? 'true' : 'false'"
              :aria-label="t('nav.menu')" @click="menuOpen = !menuOpen">
        <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor"
             stroke-width="1.7" stroke-linecap="round" aria-hidden="true">
          <path d="M4 7h16M4 12h16M4 17h16"/>
        </svg>
        <span>{{ currentLabel }}</span>
      </button>
      <h2>{{ t('admin.title') }}</h2>
      <!-- X-Admin-Token fallback row: only when the session can't reach admin data -->
      <div v-if="needToken" class="admin-token">
        <input v-model="token" type="password" :placeholder="t('admin.tokenPlaceholder')" />
        <button @click="submitToken">{{ t('admin.load') }}</button>
      </div>
      <div v-else-if="token" class="admin-token">
        <span class="muted">{{ t('admin.tokenStoredHint') }}</span>
        <button class="secondary" @click="clearStoredToken">{{ t('admin.tokenClear') }}</button>
      </div>
      <nav class="admin-nav" @click="menuOpen = false">
        <button v-for="n in visibleNav" :key="n.id" class="nav-item"
                :class="{ active: current === n.id }" @click="go(n.id)">
          <svg class="nav-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor"
               stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" v-html="n.icon"></svg>
          <span>{{ t(n.labelKey) }}</span>
        </button>
        <button v-for="p in pluginPages" :key="p.path" class="nav-item"
                :class="{ active: currentPlugin === p.path }" @click="openPlugin(p.path)">
          <svg class="nav-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor"
               stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" v-html="p.icon"></svg>
          <span>{{ pluginLabel(p.plugin, 'nav.' + p.id, p.nav.label) }}</span>
        </button>
      </nav>
    </aside>
    <main class="admin-main">
      <!-- key bump remounts the active view => its onMounted refetches -->
      <PluginPage v-if="ready && currentPlugin" :path="currentPlugin" />
      <component v-else-if="ready" :is="currentComp" :key="`${current}-${reloadTick}`" @go="go" @settings-changed="onSettingsChanged" />
      <div v-else class="card muted">{{ t('admin.enterTokenHint') }}</div>
    </main>
  </div>
</template>

<style scoped>
.admin-shell { display: flex; gap: 24px; align-items: flex-start; }
.admin-side { position: sticky; top: calc(56px + 16px); min-width: 210px; }
/* Same hierarchy as the user sidebar's .nav-title, so the theme docks align. */
.admin-side h2 {
  margin: 0 0 8px; padding-left: 12px;
  font-size: var(--fs-xs); font-weight: 600; letter-spacing: .08em;
  color: var(--muted);
}
.admin-nav { display: flex; flex-direction: column; gap: 2px; margin-top: 12px; }
/* nav items themselves come from the global .nav-item (user sidebar parity) */
.admin-token { display: flex; gap: 8px; }
.admin-token input { min-width: 0; flex: 1; }
.admin-main { flex: 1; min-width: 0; }
@media (max-width: 720px) {
  .admin-shell { flex-direction: column; }
  .admin-side { position: static; width: 100%; }
  .admin-nav { flex-direction: row; flex-wrap: wrap; }
}
</style>
