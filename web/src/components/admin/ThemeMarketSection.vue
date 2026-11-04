<script setup>
// ThemeMarketSection — signed theme catalog + local pack upload, embedded in
// the 外观 card. The server verifies registry/bundle signatures; installs and
// uninstalls hot-reload the theme registry, so we re-emit `changed` to let
// the parent refresh the theme <select>.
import { onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'
import { confirmDialog } from '../../dialog.js'
import { api } from '../../api.js'
import FilePicker from '../FilePicker.vue'

const emit = defineEmits(['changed'])
const { t } = useI18n()

const packages = ref([])
const loading = ref(false)
const loadError = ref('')
const marketOff = ref(false)
const actionError = ref('')
const busy = ref('')
const isSuper = ref(false)

const uploadFile = ref(null)
const inspection = ref(null)
const riskAck = ref(false)
const uploading = ref(false)
const uploadNote = ref('')

async function load(refresh = false) {
  loading.value = true
  loadError.value = ''
  marketOff.value = false
  try {
    const d = await fetchJSON(`/api/admin/v1/themes/market${refresh ? '?refresh=1' : ''}`)
    packages.value = d.packages || []
  } catch (e) {
    if (e.status === 404) {
      marketOff.value = true
      packages.value = []
    } else {
      loadError.value = e.message
    }
  } finally {
    loading.value = false
  }
}

function label(obj, fallback) {
  if (!obj) return fallback || ''
  for (const key of ['zh-CN', 'en']) if (obj[key]) return obj[key]
  return Object.values(obj)[0] || fallback || ''
}

async function install(p) {
  actionError.value = ''
  busy.value = p.name
  try {
    await fetchJSON('/api/admin/v1/themes/market/install', {
      method: 'POST', body: JSON.stringify({ name: p.name }),
    })
    emit('changed')
    await load()
  } catch (e) {
    actionError.value = e.message
  } finally {
    busy.value = ''
  }
}

async function uninstall(p) {
  if (!(await confirmDialog(t('admin.themeUninstallConfirm', { name: label(p.title, p.name) }), { danger: true }))) return
  actionError.value = ''
  busy.value = p.name
  try {
    await fetchJSON(`/api/admin/v1/themes/market/${encodeURIComponent(p.name)}`, { method: 'DELETE' })
    emit('changed')
    await load()
  } catch (e) {
    actionError.value = e.message
  } finally {
    busy.value = ''
  }
}

watch(uploadFile, () => {
  inspection.value = null
  riskAck.value = false
  uploadNote.value = ''
})

async function inspectUpload() {
  if (!uploadFile.value) return
  uploading.value = true
  actionError.value = ''
  inspection.value = null
  try {
    const fd = new FormData()
    fd.append('file', uploadFile.value)
    fd.append('inspect', '1')
    inspection.value = await fetchJSON('/api/admin/v1/themes/upload', { method: 'POST', body: fd })
  } catch (e) {
    actionError.value = e.message
  } finally {
    uploading.value = false
  }
}

async function commitUpload() {
  if (!uploadFile.value || !inspection.value) return
  uploading.value = true
  actionError.value = ''
  try {
    const fd = new FormData()
    fd.append('file', uploadFile.value)
    if (inspection.value.status !== 'verified') fd.append('acceptUnverified', '1')
    fd.append('expectDigest', inspection.value.zipSha || '')
    const res = await fetchJSON('/api/admin/v1/themes/upload', { method: 'POST', body: fd })
    uploadNote.value = t('admin.themeUploadDone', { name: res.id, version: res.version })
    uploadFile.value = null
    inspection.value = null
    emit('changed')
    await load()
  } catch (e) {
    actionError.value = e.message
  } finally {
    uploading.value = false
  }
}

function trustText(status) {
  return {
    verified: t('admin.themeTrustVerified'),
    unsigned: t('admin.themeTrustUnsigned'),
    'unknown-key': t('admin.themeTrustUnknownKey'),
    invalid: t('admin.themeTrustInvalid'),
  }[status] || status
}

onMounted(async () => {
  try {
    const me = await api.me()
    isSuper.value = Number(me && me.permission) === 2
  } catch { /* read-only */ }
  load()
})
</script>

<template>
  <div class="theme-market">
    <h3 class="tm-title">{{ t('admin.themeMarketTitle') }}</h3>
    <p class="muted tm-hint">{{ t('admin.themeMarketHint') }}</p>
    <div class="row" style="margin-bottom: 8px;">
      <button class="secondary small" :disabled="loading" @click="load(true)">{{ t('admin.themeRefresh') }}</button>
    </div>
    <div class="error" role="alert" v-if="loadError">{{ loadError }}</div>
    <p class="muted" v-if="loading">{{ t('common.loading') }}</p>
    <p class="muted" v-else-if="marketOff">{{ t('admin.themeMarketOff') }}</p>
    <p class="muted" v-else-if="!packages.length">{{ t('admin.themeMarketEmpty') }}</p>
    <div class="table-scroll" v-else>
      <table>
        <thead>
          <tr>
            <th>{{ t('admin.themeColTheme') }}</th>
            <th>{{ t('admin.themeColVersion') }}</th>
            <th>{{ t('admin.themeColInstalled') }}</th>
            <th>{{ t('admin.colActions') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="p in packages" :key="p.name">
            <td>
              <strong>{{ label(p.title, p.name) }}</strong>
              <div class="muted">{{ p.name }}<template v-if="p.author"> · {{ p.author }}</template></div>
            </td>
            <td>{{ p.version }}</td>
            <td>
              <span class="badge" v-if="p.installed">{{ p.installed }}</span>
              <span class="muted" v-else>—</span>
            </td>
            <td class="nowrap">
              <button v-if="!p.installed" :disabled="!isSuper || busy === p.name" @click="install(p)">{{ t('admin.themeInstall') }}</button>
              <button v-else-if="p.canUpdate" :disabled="!isSuper || busy === p.name" @click="install(p)">{{ t('admin.themeUpdate') }}</button>
              <button class="secondary danger-outline" v-if="p.installed" :disabled="!isSuper || busy === p.name" @click="uninstall(p)">{{ t('admin.themeUninstall') }}</button>
            </td>
          </tr>
        </tbody>
      </table>
    </div>
    <p class="error" role="alert" v-if="actionError">{{ actionError }}</p>


    <h3 class="tm-title">{{ t('admin.themeUploadTitle') }}</h3>
    <p class="muted tm-hint">{{ t('admin.themeUploadHint') }}</p>
    <div class="row">
      <FilePicker v-model:file="uploadFile" accept=".zip" :disabled="!isSuper"
                  :disabled-reason="t('admin.themeNeedSuper')" />
      <button class="secondary" :disabled="!uploadFile || uploading || !isSuper" @click="inspectUpload">{{ t('admin.themeInspect') }}</button>
    </div>
    <div class="card tm-inspect" v-if="inspection">
      <div class="row">
        <span class="badge" :class="inspection.status === 'verified' ? 'ok' : (inspection.status === 'invalid' || inspection.status === 'unknown-key' ? 'danger' : 'gold')">{{ trustText(inspection.status) }}</span>
        <strong v-if="inspection.theme">{{ label({ 'zh-CN': inspection.theme.name }, inspection.theme.id) }}</strong>
        <span class="muted">{{ inspection.name }}@{{ inspection.version }}</span>
      </div>
      <p class="error" role="alert" v-if="inspection.error">{{ inspection.error }}</p>
      <label class="row tm-risk" v-if="inspection.status === 'unsigned'">
        <input type="checkbox" v-model="riskAck" style="width: auto;" />
        <span>{{ t('admin.themeRiskAck') }}</span>
      </label>
      <div class="row" style="margin-top: 8px;">
        <button :disabled="uploading || !isSuper || (inspection.status === 'unsigned' && !riskAck) || inspection.status === 'invalid' || inspection.status === 'unknown-key'" @click="commitUpload">{{ t('admin.themeCommit') }}</button>
      </div>
    </div>
    <p class="success" role="status" v-if="uploadNote">{{ uploadNote }}</p>
  </div>
</template>

<style scoped>
.tm-title { font-size: var(--fs-s); margin: 18px 0 4px; color: var(--muted); letter-spacing: .06em; }
.tm-hint { margin: 0 0 10px; }
.tm-inspect { margin-top: 10px; padding: 14px 16px; }
.tm-risk { margin-top: 8px; font-size: var(--fs-s); color: var(--gold); }
</style>
