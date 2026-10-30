<script setup>
// AdminPanel shell (B1 refactor, 2026-10-24): sidebar + per-domain views.
// The old single-file card flow moved to components/admin/*; App.vue still
// mounts this at view === 'admin' (routing untouched).
import { ref, shallowRef, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../api.js'
import { token, needToken, fetchJSON, persistToken } from '../adminApi.js'
import DashboardView from './admin/DashboardView.vue'
import UsersView from './admin/UsersView.vue'
import PlayersView from './admin/PlayersView.vue'
import InvitationView from './admin/InvitationView.vue'
import SettingsView from './admin/SettingsView.vue'
import StatusView from './admin/StatusView.vue'
import UnionView from './admin/UnionView.vue'
import AccessLogsView from './admin/AccessLogsView.vue'
import PluginsView from './admin/PluginsView.vue'
import MarketView from './admin/MarketView.vue'
import PluginPage from './plugin/PluginPage.vue'
import { loadPluginUI, navPages } from '../pluginUI.js'

const { t } = useI18n()

// Sidebar navigation (blessing 管理面板 order). PlayersView is the full
// management table (B3 landed).
const nav = [
  { id: 'dashboard', labelKey: 'admin.navDashboard', comp: DashboardView },
  { id: 'users', labelKey: 'admin.navUsers', comp: UsersView },
  { id: 'players', labelKey: 'admin.navPlayers', comp: PlayersView },
  { id: 'invitation', labelKey: 'admin.navInvitation', comp: InvitationView },
  { id: 'settings', labelKey: 'admin.navSettings', comp: SettingsView },
  { id: 'status', labelKey: 'admin.navStatus', comp: StatusView },
  { id: 'access', labelKey: 'admin.navAccess', comp: AccessLogsView },
  { id: 'union', labelKey: 'admin.navUnion', comp: UnionView },
  { id: 'plugins', labelKey: 'admin.navPlugins', comp: PluginsView },
  { id: 'market', labelKey: 'admin.navMarket', comp: MarketView },
]
const current = ref('dashboard')
const currentComp = shallowRef(DashboardView)
const currentPlugin = ref('')
const pluginPages = ref([])
const myPerm = ref(0)
// Module switches hide their admin surfaces; defaults stay visible until the
// public capability endpoint answers (failure should never hide an entry).
const flags = ref({ invite_required: true, union_enabled: true })
const visibleNav = computed(() => nav.filter((n) =>
  (n.id !== 'invitation' || flags.value.invite_required) &&
  (n.id !== 'union' || flags.value.union_enabled)
))

async function loadFlags() {
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

onMounted(async () => {
  // Session-admin detection: permission > 0 means the session alone reaches
  // admin data — no token ceremony needed.
  try {
    const me = await api.me()
    myPerm.value = Number(me.permission) || 0
    if (myPerm.value > 0) needToken.value = false
  } catch { /* no usable session — the token fallback stays visible */ }
  await loadPluginUI()
  pluginPages.value = navPages('admin').filter((p) => myPerm.value >= Number(p.perm || 0))
  await loadFlags()
  if (!needToken.value || token.value) {
    ready.value = true
    reloadTick.value++
  }
})
</script>

<template>
  <div class="admin-shell">
    <aside class="card admin-side">
      <h2 style="margin-top: 0;">{{ t('admin.title') }}</h2>
      <!-- X-Admin-Token fallback row: only when the session can't reach admin data -->
      <div v-if="needToken" class="admin-token">
        <input v-model="token" type="password" :placeholder="t('admin.tokenPlaceholder')" />
        <button @click="submitToken">{{ t('admin.load') }}</button>
      </div>
      <nav class="admin-nav">
        <button v-for="n in visibleNav" :key="n.id" class="admin-nav-item"
                :class="{ active: current === n.id }" @click="go(n.id)">
          {{ t(n.labelKey) }}
        </button>
        <button v-for="p in pluginPages" :key="p.path" class="admin-nav-item"
                :class="{ active: currentPlugin === p.path }" @click="openPlugin(p.path)">
          {{ p.nav.label }}
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
.admin-shell { display: flex; gap: 20px; align-items: flex-start; }
.admin-side { position: sticky; top: calc(56px + 16px); min-width: 180px; }
.admin-nav { display: flex; flex-direction: column; gap: 2px; margin-top: 12px; }
.admin-nav-item {
  position: relative;
  text-align: left; background: none; border: 1px solid transparent;
  padding: 8px 12px; border-radius: var(--r-s); cursor: pointer; font-size: var(--fs-s);
  color: var(--muted);
  transition: background .14s var(--ease), color .14s var(--ease);
}
.admin-nav-item:hover { background: var(--hover-wash); color: var(--text); }
.admin-nav-item.active {
  background: var(--accent-wash); color: var(--text); font-weight: 600;
}
.admin-nav-item.active::before {
  content: ""; position: absolute; left: 0; top: 8px; bottom: 8px;
  width: 3px; border-radius: 2px; background: var(--accent);
}
.admin-token { display: flex; gap: 8px; }
.admin-token input { min-width: 0; flex: 1; }
.admin-main { flex: 1; min-width: 0; }
@media (max-width: 720px) {
  .admin-shell { flex-direction: column; }
  .admin-side { position: static; width: 100%; }
  .admin-nav { flex-direction: row; flex-wrap: wrap; }
  .admin-nav-item.active::before { display: none; }
}
</style>
