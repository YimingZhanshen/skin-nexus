<script setup>
// Signed plugin market. The server verifies the registry and artifacts; this
// view only renders the verified catalog and sends super-admin actions.
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'
import { api } from '../../api.js'
import { loadPluginUI } from '../../pluginUI.js'
import Pager from '../Pager.vue'
import EmptyState from '../EmptyState.vue'

const { t } = useI18n()
const packages = ref([])
const loading = ref(false)
const loadError = ref('')
const actionError = ref('')
const search = ref('')
const page = ref(1)
const pageSize = 10
const installings = ref(new Set())
const selected = ref(null)
const consent = ref(false)
const readme = ref('')
const isSuper = ref(false)
const readOnly = ref(true)
const uploadFile = ref(null)
const uploading = ref(false)
const inspection = ref(null)
const riskAck = ref(false)

const filtered = computed(() => {
  const q = search.value.trim().toLowerCase()
  return packages.value.filter((p) => !q || p.name.includes(q) || Object.values(p.title || {}).join(' ').toLowerCase().includes(q))
})
const totalPages = computed(() => Math.max(1, Math.ceil(filtered.value.length / pageSize)))
const paged = computed(() => filtered.value.slice((page.value - 1) * pageSize, page.value * pageSize))

async function load(refresh = false) {
  loading.value = true
  loadError.value = ''
  try {
    const d = await fetchJSON(`/api/admin/v1/market/catalog${refresh ? '?refresh=1' : ''}`)
    packages.value = d.packages || []
  } catch (e) {
    loadError.value = e.message
  } finally {
    loading.value = false
  }
}

function label(obj, fallback) {
  if (!obj) return fallback || ''
  for (const key of ['zh-CN', 'en']) if (obj[key]) return obj[key]
  return Object.values(obj)[0] || fallback || ''
}

function planOf(p) {
  return p.plan || {}
}

function canAct(p) {
  const plan = planOf(p)
  return plan.canInstall !== false && (p.canUpdate || !p.installed)
}

function openInstall(p) {
  selected.value = p
  consent.value = false
  actionError.value = ''
  readme.value = p.readme || p.changelog || ''
}

function onFile(e) {
  uploadFile.value = e.target.files && e.target.files[0]
  inspection.value = null
  riskAck.value = false
}

function trustText(status) {
  switch (status) {
    case 'verified': return t('admin.trustVerified')
    case 'unknown-key': return t('admin.trustUnknownKey')
    case 'unsigned': return t('admin.trustUnsigned')
    case 'invalid': return t('admin.trustInvalid')
    default: return t('admin.trustUnrecorded')
  }
}

async function uploadBundle() {
  if (!uploadFile.value) return
  uploading.value = true
  actionError.value = ''
  try {
    const fd = new FormData()
    fd.append('file', uploadFile.value)
    fd.append('inspect', '1')
    const r = await fetch('/api/admin/v1/market/upload', { method: 'POST', body: fd, credentials: 'same-origin' })
    const d = await r.json().catch(() => ({}))
    if (!r.ok) throw new Error(d.error || 'inspect failed')
    inspection.value = d
    riskAck.value = false
  } catch (e) {
    actionError.value = e.message
  } finally {
    uploading.value = false
  }
}

function cancelInspection() {
  inspection.value = null
  riskAck.value = false
  uploadFile.value = null
}

async function installInspected() {
  if (!inspection.value || !uploadFile.value) return
  if (inspection.value.status !== 'verified' && !riskAck.value) return
  uploading.value = true
  actionError.value = ''
  try {
    const fd = new FormData()
    fd.append('file', uploadFile.value)
    fd.append('consent', '1')
    fd.append('acceptUnverified', inspection.value.status === 'verified' ? '0' : '1')
    fd.append('expectDigest', inspection.value.zipSha || '')
    const r = await fetch('/api/admin/v1/market/upload', { method: 'POST', body: fd, credentials: 'same-origin' })
    const d = await r.json().catch(() => ({}))
    if (!r.ok) throw new Error(d.error || 'install failed')
    cancelInspection()
    await load()
  } catch (e) {
    actionError.value = e.message
  } finally {
    uploading.value = false
  }
}

async function confirmInstall() {
  if (!selected.value) return
  const p = selected.value
  const plan = planOf(p)
  if (plan.addedPermissions && plan.addedPermissions.length && !consent.value) return
  installings.value.add(p.name)
  actionError.value = ''
  try {
    const d = await fetchJSON('/api/admin/v1/market/install', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: p.name, consent: consent.value }),
    })
    selected.value = null
    await load()
    await loadPluginUI()
    if (d && d.state) actionError.value = ''
  } catch (e) {
    actionError.value = e.message
  } finally {
    installings.value.delete(p.name)
    installings.value = new Set(installings.value)
  }
}

onMounted(async () => {
  try {
    const me = await api.me()
    isSuper.value = Number(me.permission) === 2
  } catch { /* read-only */ }
  readOnly.value = !isSuper.value
  await load()
})
</script>

<template>
  <div class="card">
    <div class="row" style="justify-content: space-between; align-items: center;">
      <h2 style="margin-top: 0;">{{ t('admin.marketTitle') }}</h2>
      <button class="secondary" :disabled="loading" @click="load(true)">{{ t('admin.marketRefresh') }}</button>
    </div>
    <p class="muted">{{ t('admin.marketHint') }}</p>
    <p class="muted" v-if="readOnly">{{ t('admin.marketReadOnly') }}</p>
    <div class="error" v-if="loadError">{{ loadError }}</div>
    <div class="error" v-if="actionError">{{ actionError }}</div>

    <div class="row" style="margin: 12px 0; gap: 8px; align-items: center;">
      <input v-model="search" :placeholder="t('admin.marketSearch')" style="max-width: 360px;" @input="page = 1" />
      <input type="file" accept=".zip" :disabled="!isSuper || uploading" @change="onFile" />
      <button class="secondary" :disabled="!isSuper || !uploadFile || uploading" @click="uploadBundle">
        {{ uploading ? t('admin.marketInstalling') : t('admin.marketUpload') }}
      </button>
    </div>

    <div v-if="inspection" class="card" style="margin: 12px 0;">
      <h3 style="margin-top: 0;">{{ inspection.name }} <span class="muted">{{ inspection.version }}</span></h3>
      <p>
        <span class="badge" :class="{ danger: inspection.status !== 'verified' }">{{ trustText(inspection.status) }}</span>
        <span v-if="inspection.keyId" class="muted" style="margin-left: 8px;">{{ t('admin.marketSigner') }}: {{ inspection.keyId }}</span>
      </p>
      <p class="muted" style="font-size: var(--fs-xs); word-break: break-all;">
        {{ t('admin.marketDigest') }}: {{ inspection.zipSha }}
      </p>
      <ul v-if="inspection.permissions && inspection.permissions.length">
        <li v-for="perm in inspection.permissions" :key="perm"><code>{{ perm }}</code></li>
      </ul>
      <div v-if="inspection.status !== 'verified'" class="error">
        <p style="margin-top: 0;"><strong>{{ t('admin.marketRiskTitle') }}</strong></p>
        <p>{{ t('admin.marketRiskBody') }}</p>
        <p v-if="inspection.reason" class="muted">{{ inspection.reason }}</p>
        <label class="row" style="align-items: center; gap: 8px;">
          <input v-model="riskAck" type="checkbox" />
          <span>{{ t('admin.marketRiskAck') }}</span>
        </label>
      </div>
      <div class="row" style="margin-top: 12px;">
        <button :disabled="uploading || (inspection.status !== 'verified' && !riskAck)" @click="installInspected">
          {{ uploading ? t('admin.marketInstalling') : t('admin.marketInstall') }}
        </button>
        <button class="secondary" :disabled="uploading" @click="cancelInspection">{{ t('common.cancel') }}</button>
      </div>
    </div>

    <p v-if="loading" class="muted">{{ t('common.loading') }}</p>
    <EmptyState v-else-if="!filtered.length" :text="t('admin.marketEmpty')" />
    <div class="table-scroll" v-else>
    <table>
      <thead>
        <tr>
          <th>{{ t('admin.colPluginName') }}</th>
          <th>{{ t('admin.marketDescription') }}</th>
          <th>{{ t('admin.colVersion') }}</th>
          <th>{{ t('admin.marketInstalled') }}</th>
          <th>{{ t('admin.colActions') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="p in paged" :key="p.name">
          <td>
            <strong>{{ label(p.title, p.name) }}</strong>
            <div class="muted">{{ p.name }} · {{ p.author || '—' }}</div>
          </td>
          <td class="muted">{{ label(p.description) }}</td>
          <td>{{ p.version }}</td>
          <td>
            <span v-if="!p.installed" class="badge">{{ t('admin.marketNotInstalled') }}</span>
            <span v-else-if="p.canUpdate" class="badge">{{ p.installed }} → {{ p.version }}</span>
            <span v-else class="badge">{{ p.installed }}</span>
            <div v-if="planOf(p).missing && planOf(p).missing.length" class="error" style="font-size: var(--fs-xs);">
              {{ t('admin.marketMissing') }}: {{ planOf(p).missing.join('; ') }}
            </div>
            <div v-if="planOf(p).conflicts && planOf(p).conflicts.length" class="error" style="font-size: var(--fs-xs);">
              {{ t('admin.marketConflicts') }}: {{ planOf(p).conflicts.join('; ') }}
            </div>
          </td>
          <td class="nowrap">
            <button v-if="!p.installed" :disabled="!isSuper || !canAct(p) || installings.has(p.name)" @click="openInstall(p)">
              {{ installings.has(p.name) ? t('admin.marketInstalling') : t('admin.marketInstall') }}
            </button>
            <button v-else-if="p.canUpdate" :disabled="!isSuper || !canAct(p) || installings.has(p.name)" @click="openInstall(p)">
              {{ installings.has(p.name) ? t('admin.marketUpdating') : t('admin.marketUpdate') }}
            </button>
          </td>
        </tr>
      </tbody>
    </table>
    </div>
    <Pager :page="page" :pages="totalPages" align="end" @update:page="page = $event" />

    <div v-if="selected" class="card" style="margin-top: 16px;">
      <h3>{{ t('admin.marketInstallTitle', { name: label(selected.title, selected.name) }) }}</h3>
      <p class="muted">{{ t('admin.marketInstallHint') }}</p>
      <div class="md" v-if="readme" style="max-height: 260px; overflow: auto; white-space: pre-wrap;">{{ readme }}</div>
      <ul v-if="planOf(selected).addedPermissions && planOf(selected).addedPermissions.length">
        <li v-for="perm in planOf(selected).addedPermissions" :key="perm"><code>{{ perm }}</code></li>
      </ul>
      <ul v-if="planOf(selected).missing && planOf(selected).missing.length" class="error">
        <li v-for="m in planOf(selected).missing" :key="m">{{ m }}</li>
      </ul>
      <ul v-if="planOf(selected).conflicts && planOf(selected).conflicts.length" class="error">
        <li v-for="c in planOf(selected).conflicts" :key="c">{{ c }}</li>
      </ul>
      <label v-if="planOf(selected).addedPermissions && planOf(selected).addedPermissions.length" class="row" style="align-items:center;gap:8px;">
        <input v-model="consent" type="checkbox" />
        <span>{{ t('admin.marketConsent') }}</span>
      </label>
      <div class="row" style="margin-top: 12px;">
        <button :disabled="!isSuper || installings.has(selected.name)" @click="confirmInstall">
          {{ selected.installed ? t('admin.marketUpdate') : t('admin.marketInstall') }}
        </button>
        <button class="secondary" @click="selected = null">{{ t('common.cancel') }}</button>
      </div>
    </div>
  </div>
</template>
