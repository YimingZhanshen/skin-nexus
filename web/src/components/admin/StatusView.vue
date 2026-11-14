<script setup>
// StatusView: read-only runtime facts (运行状态, blessing parity B1) —
// build metadata, runtime/environment, site paths, dependencies and modules —
// plus the deployment self-check (U16), which reads live configuration rather
// than the boot snapshot and is therefore fetched separately.
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'
import { formatWhen } from '../../format.js'

const { t } = useI18n()
const status = ref(null)
const error = ref('')

const diag = ref(null)
const diagError = ref('')
const diagBusy = ref(false)
const mailBusy = ref(false)
const mailMsg = ref('')
const mailErr = ref('')

const build = computed(() => {
  const s = status.value || {}
  return s.build || { version: s.version || '', commit: '', time: '', branch: '' }
})
const runtime = computed(() => (status.value && status.value.runtime) || {})
const environment = computed(() => (status.value && status.value.environment) || {})
const modules = computed(() => (status.value && status.value.modules) || {})

const diagClass = { ok: 'ok', warn: 'gold', fail: 'danger', skip: 'dim' }
const diagStateClass = (s) => diagClass[s] || 'dim'

const shortCommit = (v) => (v && v !== 'unknown' ? String(v).slice(0, 12) : '—')
const show = (v) => (v === undefined || v === null || v === '' ? '—' : String(v))

function formatTime(v) {
  if (!v || v === 'unknown') return '—'
  return formatWhen(v)
}

function formatUptime(sec) {
  const n = Number(sec)
  if (!Number.isFinite(n) || n < 0) return '—'
  const d = Math.floor(n / 86400)
  const h = Math.floor((n % 86400) / 3600)
  const m = Math.floor((n % 3600) / 60)
  const s = Math.floor(n % 60)
  if (d) return `${d}d ${h}h ${m}m`
  if (h) return `${h}h ${m}m`
  if (m) return `${m}m ${s}s`
  return `${s}s`
}

async function loadDiag() {
  if (diagBusy.value) return
  diagBusy.value = true
  diagError.value = ''
  try {
    diag.value = await fetchJSON('/api/admin/v1/diagnostics')
  } catch (e) { diagError.value = e.message } finally { diagBusy.value = false }
}

// The endpoint takes no recipient (it is the caller's own address), so there is
// nothing to pass and nothing to relay.
async function sendTestMail() {
  if (mailBusy.value) return
  mailBusy.value = true
  mailMsg.value = ''
  mailErr.value = ''
  try {
    const d = await fetchJSON('/api/admin/v1/mail-test', { method: 'POST' })
    mailMsg.value = t('admin.diagMailSent', { to: d.to, host: d.host })
  } catch (e) { mailErr.value = e.message } finally { mailBusy.value = false }
}

onMounted(async () => {
  error.value = ''
  try {
    status.value = await fetchJSON('/api/admin/v1/status')
  } catch (e) { error.value = e.message }
  loadDiag()
})
</script>

<template>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('admin.statusTitle') }}</h2>
    <div class="error" role="alert" v-if="error">{{ error }}</div>

    <template v-if="status">
      <section class="status-section">
        <h3>{{ t('admin.statusBuildTitle') }}</h3>
        <div class="status-grid">
          <div class="status-item"><span class="k">{{ t('admin.statusVersion') }}</span><span class="v">{{ show(build.version) }}</span></div>
          <div class="status-item"><span class="k">{{ t('admin.statusCommit') }}</span><span class="v" :title="build.commit">{{ shortCommit(build.commit) }}</span></div>
          <div class="status-item"><span class="k">{{ t('admin.statusBuildTime') }}</span><span class="v">{{ formatTime(build.time) }}</span></div>
          <div class="status-item"><span class="k">{{ t('admin.statusBranch') }}</span><span class="v">{{ show(build.branch) }}</span></div>
        </div>
      </section>

      <section class="status-section">
        <h3>{{ t('admin.statusRuntimeTitle') }}</h3>
        <div class="status-grid">
          <div class="status-item"><span class="k">{{ t('admin.statusEnvironment') }}</span><span class="v">{{ show(environment.name) }}</span></div>
          <div class="status-item"><span class="k">{{ t('admin.statusGo') }}</span><span class="v">{{ show(runtime.goVersion || status.goVersion) }}</span></div>
          <div class="status-item"><span class="k">{{ t('admin.statusOS') }}</span><span class="v">{{ show(runtime.os) }} / {{ show(runtime.arch) }}</span></div>
          <div class="status-item"><span class="k">{{ t('admin.statusHostname') }}</span><span class="v">{{ show(runtime.hostname) }}</span></div>
          <div class="status-item"><span class="k">{{ t('admin.statusContainer') }}</span><span class="v">{{ show(runtime.container) }}</span></div>
          <div class="status-item"><span class="k">{{ t('admin.statusPID') }}</span><span class="v">{{ show(runtime.pid) }}</span></div>
          <div class="status-item"><span class="k">{{ t('admin.statusStarted') }}</span><span class="v">{{ formatTime(runtime.startedAt) }}</span></div>
          <div class="status-item"><span class="k">{{ t('admin.statusUptime') }}</span><span class="v">{{ formatUptime(runtime.uptimeSeconds) }}</span></div>
        </div>
      </section>

      <section class="status-section">
        <h3>{{ t('admin.statusSiteTitle') }}</h3>
        <div class="status-grid">
          <div class="status-item"><span class="k">{{ t('admin.statusSiteName') }}</span><span class="v">{{ show(environment.siteName) }}</span></div>
          <div class="status-item"><span class="k">{{ t('admin.statusSiteURL') }}</span><span class="v">{{ show(environment.baseURL) }}</span></div>
          <div class="status-item"><span class="k">{{ t('admin.statusListen') }}</span><span class="v">{{ show(environment.listen) }}</span></div>
          <div class="status-item"><span class="k">{{ t('admin.statusDataDir') }}</span><span class="v" :title="environment.dataDir">{{ show(environment.dataDir) }}</span></div>
          <div class="status-item"><span class="k">{{ t('admin.statusMigrationsDir') }}</span><span class="v" :title="environment.migrationsDir">{{ show(environment.migrationsDir) }}</span></div>
        </div>
      </section>

      <section class="status-section">
        <h3>{{ t('admin.statusDepsTitle') }}</h3>
        <div class="status-grid">
          <div class="status-item"><span class="k">{{ t('admin.statusDatabase') }}</span><span class="v">{{ show(status.dbEngine) }} {{ show(status.dbVersion) }}</span></div>
          <div class="status-item"><span class="k">{{ t('admin.statusMigration') }}</span><span class="v">{{ show(status.migration) }}</span></div>
          <div class="status-item"><span class="k">{{ t('admin.statusMailFailures') }}</span><span class="v">{{ show(status.mailFailures24h) }}</span></div>
        </div>
        <div class="status-badges">
          <span class="badge" :class="{ dim: !modules.union }">
            {{ t('admin.statusUnion') }} {{ modules.union ? t('admin.moduleOn') : t('admin.moduleOff') }}
          </span>
          <span class="badge" :class="{ dim: !modules.appearance }">
            {{ t('admin.statusAppearance') }} {{ modules.appearance ? t('admin.moduleOn') : t('admin.moduleOff') }}
          </span>
          <span class="badge" :class="{ dim: !modules.oidc }">
            {{ t('admin.statusOIDC') }} {{ modules.oidc ? t('admin.moduleOn') : t('admin.moduleOff') }}
          </span>
          <span class="badge">{{ t('admin.signKeyFingerprint') }} {{ show(status.signKeyFingerprint) }}</span>
        </div>
      </section>
    </template>

    <section class="status-section">
      <div class="diag-head">
        <h3>{{ t('admin.diagTitle') }}</h3>
        <button class="secondary" :disabled="diagBusy" @click="loadDiag">
          {{ diagBusy ? t('admin.diagChecking') : t('admin.diagRecheck') }}
        </button>
      </div>
      <div class="muted diag-intro">{{ t('admin.diagIntro') }}</div>
      <div class="error" role="alert" v-if="diagError">{{ diagError }}</div>
      <template v-if="diag && diag.items">
        <div class="diag-row" v-for="it in diag.items" :key="it.id">
          <span class="badge" :class="diagStateClass(it.state)">{{ t('admin.diagState.' + it.state) }}</span>
          <div class="diag-body">
            <div class="diag-name">{{ t('admin.diag.' + it.id) }}</div>
            <div class="diag-value" v-if="it.value">{{ it.value }}</div>
            <div class="diag-fix" v-if="it.state !== 'ok' && it.fix">{{ t('admin.diagFix.' + it.fix) }}</div>
            <template v-if="it.id === 'mail'">
              <button class="secondary" :disabled="mailBusy" @click="sendTestMail">
                {{ mailBusy ? t('admin.diagMailTesting') : t('admin.diagMailTest') }}
              </button>
              <div class="success" role="status" v-if="mailMsg">{{ mailMsg }}</div>
              <div class="error" role="alert" v-if="mailErr">{{ mailErr }}</div>
            </template>
          </div>
        </div>
        <div class="muted diag-at">{{ t('admin.diagCheckedAt') }} {{ formatTime(diag.checked_at) }}</div>
      </template>
    </section>
  </div>
</template>

<style scoped>
.status-section { margin-top: 24px; padding-top: 20px; border-top: 1px solid var(--line); }
.status-section:first-of-type { margin-top: 0; padding-top: 0; border-top: 0; }
.status-section h3 { margin: 0 0 12px; font-size: var(--fs-m); }
.status-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(210px, 1fr)); gap: 12px 24px; }
.status-item { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.status-item .k { color: var(--muted); font-size: var(--fs-xs); }
.status-item .v { color: var(--text); font-size: var(--fs-s); overflow-wrap: anywhere; }
.status-badges { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 16px; }
.diag-head { display: flex; align-items: center; justify-content: space-between; gap: 12px; flex-wrap: wrap; }
.diag-head h3 { margin: 0; }
.diag-intro { font-size: var(--fs-xs); margin: 8px 0 16px; }
.diag-row { display: flex; gap: 12px; align-items: flex-start; padding: 12px 0; border-top: 1px solid var(--line); }
.diag-row:first-of-type { border-top: 0; }
.diag-row .badge { flex: 0 0 auto; min-width: 5.5em; text-align: center; }
.diag-body { min-width: 0; display: flex; flex-direction: column; gap: 4px; }
.diag-name { font-size: var(--fs-s); color: var(--text); }
.diag-value { font-size: var(--fs-xs); color: var(--muted); overflow-wrap: anywhere; }
.diag-fix { font-size: var(--fs-xs); color: var(--text); }
.diag-at { font-size: var(--fs-xs); margin-top: 12px; }
</style>
