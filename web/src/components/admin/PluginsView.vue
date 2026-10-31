<script setup>
// PluginsView: read-only catalog for admins; enable/disable/reload are
// SUPER_ADMIN-session-only (the backend rejects X-Admin-Token and perm<2).
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON, needToken } from '../../adminApi.js'
import { api } from '../../api.js'
import { loadPluginUI } from '../../pluginUI.js'
import SchemaForm from '../SchemaForm.vue'

const { t } = useI18n()

const plugins = ref([])
const loadError = ref('')
const actionError = ref('')
const isSuper = ref(false)
const readOnly = ref(true)

const configTarget = ref(null)
const configView = ref(null)
const configValues = ref({})
const configError = ref('')
const configOk = ref('')
const configSaving = ref(false)
const guardsBypassed = ref(false)

async function load() {
  loadError.value = ''
  try {
    const d = await fetchJSON('/api/admin/v1/plugins')
    plugins.value = d.plugins || []
    guardsBypassed.value = !!d.guardsBypassed
  } catch (e) {
    loadError.value = e.message
  }
}

async function detectRole() {
  // Token-only sessions (machine fallback) can list but never mutate.
  if (needToken.value) return
  try {
    const me = await api.me()
    isSuper.value = Number(me.permission) === 2
  } catch { /* stay read-only */ }
}

async function openConfig(p) {
  configTarget.value = p
  configView.value = null
  configValues.value = {}
  configError.value = ''
  configOk.value = ''
  try {
    const d = await fetchJSON(`/api/admin/v1/plugins/${encodeURIComponent(p.name)}/config`)
    configView.value = d
    configValues.value = { ...(d.values || {}) }
  } catch (e) {
    configError.value = e.message
  }
}

async function saveConfig() {
  if (!configTarget.value || !configView.value) return
  configSaving.value = true
  configError.value = ''
  configOk.value = ''
  try {
    const d = await fetchJSON(`/api/admin/v1/plugins/${encodeURIComponent(configTarget.value.name)}/config`, {
      method: 'PUT', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ values: configValues.value }),
    })
    configView.value = d
    configValues.value = { ...(d.values || {}) }
    configOk.value = t('admin.pluginsConfigSaved')
    await load()
  } catch (e) {
    configError.value = e.message
  } finally {
    configSaving.value = false
  }
}

async function rollback(p) {
  actionError.value = ''
  if (!window.confirm(t('admin.pluginsRollbackConfirm', { name: p.title || p.name }))) return
  try {
    await fetchJSON(`/api/admin/v1/plugins/${encodeURIComponent(p.name)}/rollback`, { method: 'POST' })
    await load()
    await loadPluginUI()
  } catch (e) { actionError.value = e.message }
}

async function uninstall(p) {
  actionError.value = ''
  if (!window.confirm(t('admin.pluginsUninstallConfirm', { name: p.title || p.name }))) return
  try {
    await fetchJSON(`/api/admin/v1/plugins/${encodeURIComponent(p.name)}`, { method: 'DELETE' })
    await load()
    await loadPluginUI()
  } catch (e) { actionError.value = e.message }
}

async function toggleBypass() {
  actionError.value = ''
  const next = !guardsBypassed.value
  if (next && !window.confirm(t('admin.pluginsGuardBypassConfirm'))) return
  try {
    const d = await fetchJSON('/api/admin/v1/plugins/guards/bypass', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ enabled: next }),
    })
    guardsBypassed.value = !!d.guardsBypassed
  } catch (e) { actionError.value = e.message }
}

async function act(p, action) {
  actionError.value = ''
  if (action === 'enable' && !window.confirm(t('admin.pluginsEnableConfirm'))) return
  try {
    await fetchJSON(`/api/admin/v1/plugins/${encodeURIComponent(p.name)}/${action}`, { method: 'POST' })
    await load()
    await loadPluginUI()
  } catch (e) {
    actionError.value = e.message
  }
}

function trustText(p) {
  switch (p.trust) {
    case 'verified': return t('admin.trustVerified')
    case 'unknown-key': return t('admin.trustUnknownKey')
    case 'unsigned': return t('admin.trustUnsigned')
    case 'invalid': return t('admin.trustInvalid')
    default: return t('admin.trustUnrecorded')
  }
}

function sourceText(p) {
  switch (p.source) {
    case 'market': return t('admin.sourceMarket')
    case 'upload': return t('admin.sourceUpload')
    case 'manual': return t('admin.sourceManual')
    default: return '—'
  }
}

function provenanceTitle(p) {
  return [p.signer ? `${t('admin.marketSigner')}: ${p.signer}` : '', p.digest ? `${t('admin.marketDigest')}: ${p.digest}` : '']
    .filter(Boolean).join('\n')
}

function shownPerms(p) { return (p.permissions || []).slice(0, 5) }
function morePerms(p) { return Math.max(0, (p.permissions || []).length - 5) }

onMounted(async () => {
  await detectRole()
  readOnly.value = !isSuper.value
  await load()
})
</script>

<template>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('admin.pluginsTitle') }}</h2>
    <p class="muted">{{ t('admin.pluginsHint') }}</p>
    <p class="muted" v-if="readOnly">{{ t('admin.pluginsReadOnly') }}</p>
    <label v-if="isSuper" class="row" style="align-items:center;gap:8px;margin:8px 0;">
      <input type="checkbox" :checked="guardsBypassed" @change="toggleBypass" />
      <span class="muted">{{ t('admin.pluginsGuardBypass') }}</span>
    </label>
    <div class="error" v-if="loadError">{{ loadError }}</div>
    <div class="error" v-if="actionError">{{ actionError }}</div>

    <p v-if="!plugins.length" class="muted">{{ t('admin.pluginsEmpty') }}</p>
    <div v-else class="plugin-list">
      <div v-for="p in plugins" :key="p.name" class="plugin-row">
        <div class="plugin-info">
          <div class="plugin-title-row">
            <strong>{{ p.title || p.name }}</strong>
            <span class="badge">v{{ p.version }}</span>
            <span class="badge" :class="{ danger: p.trust && p.trust !== 'verified' }"
                  :title="provenanceTitle(p)">{{ trustText(p) }}</span>
            <span class="muted">{{ sourceText(p) }}</span>
          </div>
          <div class="plugin-desc">{{ p.description }}</div>
          <div class="plugin-meta">
            <span><span class="meta-label">{{ t('admin.colAuthor') }}</span>{{ p.author || '—' }}</span>
            <span>
              <span class="meta-label">{{ t('admin.colState') }}</span>
              <span class="badge">{{ t('admin.state' + p.state.charAt(0).toUpperCase() + p.state.slice(1)) }}</span>
            </span>
            <span>
              <span class="meta-label">{{ t('admin.colHealth') }}</span>
              <span class="badge" :class="{ 'badge-ok': (p.health || p.state) === 'ok' }">{{ p.health || p.state }}</span>
            </span>
            <span><span class="meta-label">{{ t('admin.colRoutes') }}</span>{{ p.routes || 0 }} · {{ p.uiPages || 0 }} UI</span>
            <span v-if="p.lastError" class="error-text" :title="p.lastError">
              <span class="meta-label">{{ t('admin.colError') }}</span>{{ p.lastError }}
            </span>
          </div>
          <div class="plugin-perms">
            <span class="meta-label">{{ t('admin.colPerms') }}</span>
            <span class="badge" v-for="perm in shownPerms(p)" :key="perm">{{ perm }}</span>
            <span v-if="morePerms(p)" class="badge" :title="(p.permissions || []).join(', ')">+{{ morePerms(p) }}</span>
          </div>
        </div>
        <div class="plugin-actions">
          <button class="secondary" @click="openConfig(p)">{{ t('admin.pluginsConfig') }}</button>
          <template v-if="!readOnly">
            <button v-if="!p.enabled" @click="act(p, 'enable')">{{ t('admin.pluginsEnable') }}</button>
            <button v-else class="secondary" @click="act(p, 'disable')">{{ t('admin.pluginsDisable') }}</button>
            <button class="secondary" @click="act(p, 'reload')">{{ t('admin.pluginsReload') }}</button>
            <button class="secondary" @click="rollback(p)">{{ t('admin.pluginsRollback') }}</button>
            <button class="danger-outline" @click="uninstall(p)">{{ t('admin.pluginsUninstall') }}</button>
          </template>
        </div>
      </div>
    </div>

    <div v-if="configTarget" class="card" style="margin-top: 16px;">
      <div class="row" style="justify-content: space-between; align-items: center;">
        <h3 style="margin: 0;">{{ t('admin.pluginsConfigTitle', { name: configTarget.title || configTarget.name }) }}</h3>
        <button class="secondary" @click="configTarget = null">{{ t('common.close') }}</button>
      </div>
      <p class="muted">{{ t('admin.pluginsConfigHint') }}</p>
      <div class="error" v-if="configError">{{ configError }}</div>
      <div class="success" v-if="configOk">{{ configOk }}</div>
      <template v-if="configView">
        <SchemaForm :schema="configView.schema" v-model="configValues" :disabled="!isSuper || configSaving" />
        <div class="row" style="margin-top: 12px;">
          <button :disabled="!isSuper || configSaving" @click="saveConfig">{{ t('admin.pluginsConfigSave') }}</button>
          <span v-if="!isSuper" class="muted">{{ t('admin.pluginsConfigReadOnly') }}</span>
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.plugin-list { display: flex; flex-direction: column; gap: 12px; margin-top: 12px; }
.plugin-row {
  display: grid;
  grid-template-columns: minmax(0, 1fr) auto;
  gap: 16px;
  padding: 16px;
  border: 1px solid var(--border);
  border-radius: var(--r-l);
  background: var(--ink-3);
  align-items: start;
  transition: border-color var(--t-fast) var(--ease);
}
.plugin-row:hover { border-color: var(--line-strong); }
.plugin-info { min-width: 0; }
.plugin-title-row { display: flex; flex-wrap: wrap; align-items: center; gap: 8px; }
.plugin-title-row strong { font-size: var(--fs-m); }
.plugin-desc { margin: 6px 0 10px; color: var(--muted); font-size: var(--fs-s); line-height: 1.6; }
.plugin-meta { display: flex; flex-wrap: wrap; gap: 8px 18px; font-size: var(--fs-xs); color: var(--muted); align-items: center; }
.plugin-meta > span { display: inline-flex; align-items: center; gap: 6px; min-width: 0; }
.meta-label { color: var(--muted); opacity: .72; }
.plugin-perms { display: flex; flex-wrap: wrap; gap: 6px; align-items: center; margin-top: 10px; }
.plugin-actions { display: flex; flex-wrap: wrap; gap: 8px; align-content: flex-start; justify-content: flex-end; max-width: 280px; }
.plugin-actions .danger-outline { border-color: var(--danger); color: var(--danger); }
.plugin-actions .danger-outline:hover { background: var(--danger); color: var(--on-danger); }
.error-text { color: var(--danger); max-width: 360px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.badge-ok { color: var(--ok); border-color: var(--ok); }
@media (max-width: 900px) {
  .plugin-row { grid-template-columns: 1fr; }
  .plugin-actions { justify-content: flex-start; max-width: none; }
}
</style>
