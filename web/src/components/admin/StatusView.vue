<script setup>
// StatusView: read-only runtime facts (运行状态, blessing parity B1) —
// build metadata, runtime/environment, site paths, dependencies and modules.
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'

const { t } = useI18n()
const status = ref(null)
const error = ref('')

const build = computed(() => {
  const s = status.value || {}
  return s.build || { version: s.version || '', commit: '', time: '', branch: '' }
})
const runtime = computed(() => (status.value && status.value.runtime) || {})
const environment = computed(() => (status.value && status.value.environment) || {})
const modules = computed(() => (status.value && status.value.modules) || {})

const shortCommit = (v) => (v && v !== 'unknown' ? String(v).slice(0, 12) : '—')
const show = (v) => (v === undefined || v === null || v === '' ? '—' : String(v))

function formatTime(v) {
  if (!v || v === 'unknown') return '—'
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? String(v) : d.toLocaleString()
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

onMounted(async () => {
  error.value = ''
  try {
    status.value = await fetchJSON('/api/admin/v1/status')
  } catch (e) { error.value = e.message }
})
</script>

<template>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('admin.statusTitle') }}</h2>
    <div class="error" v-if="error">{{ error }}</div>

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
          <div class="status-item"><span class="k">{{ t('admin.statusMySQL') }}</span><span class="v">{{ show(status.mysqlVersion) }}</span></div>
          <div class="status-item"><span class="k">{{ t('admin.statusMigration') }}</span><span class="v">{{ show(status.migration) }}</span></div>
        </div>
        <div class="status-badges">
          <span class="badge" :style="modules.union ? '' : 'opacity: 0.6;'">
            {{ t('admin.statusUnion') }} {{ modules.union ? t('admin.moduleOn') : t('admin.moduleOff') }}
          </span>
          <span class="badge" :style="modules.appearance ? '' : 'opacity: 0.6;'">
            {{ t('admin.statusAppearance') }} {{ modules.appearance ? t('admin.moduleOn') : t('admin.moduleOff') }}
          </span>
          <span class="badge" :style="modules.oidc ? '' : 'opacity: 0.6;'">
            {{ t('admin.statusOIDC') }} {{ modules.oidc ? t('admin.moduleOn') : t('admin.moduleOff') }}
          </span>
          <span class="badge">{{ t('admin.signKeyFingerprint') }} {{ show(status.signKeyFingerprint) }}</span>
        </div>
      </section>
    </template>
  </div>
</template>

<style scoped>
.status-section { margin-top: 22px; padding-top: 18px; border-top: 1px solid var(--line); }
.status-section:first-of-type { margin-top: 0; padding-top: 0; border-top: 0; }
.status-section h3 { margin: 0 0 12px; font-size: var(--fs-m); }
.status-grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(210px, 1fr)); gap: 10px 24px; }
.status-item { display: flex; flex-direction: column; gap: 2px; min-width: 0; }
.status-item .k { color: var(--muted); font-size: var(--fs-xs); }
.status-item .v { color: var(--text); font-size: var(--fs-s); overflow-wrap: anywhere; }
.status-badges { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 14px; }
</style>
