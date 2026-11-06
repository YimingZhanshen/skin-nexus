<script setup>
// AccessLogsView (2026-10-26 观测面): web/launcher logins, Yggdrasil
// server traffic and Union traffic in one filterable page — the admin's
// "what is happening right now" view. Read-only; retention is server-side
// (90 days). Diagnostic-mode switch (2026-11-05, slice 5b): super admin only.
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'
import Pager from '../Pager.vue'

const { t } = useI18n()

const logs = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 50
const kind = ref('')
const query = ref('')
const error = ref('')

const KINDS = [
  'web_login', 'web_register',
  'ygg_authenticate', 'ygg_refresh', 'ygg_validate',
  'ygg_join', 'ygg_hasjoined', 'ygg_profile',
  'union_in', 'union_out',
]

// Diagnostic-mode switch state (GET /access-log/detail-mode).
const detailEnabled = ref(false)
const detailUntil = ref('')
const detailRemaining = ref(0)
const detailError = ref('')
const detailBusy = ref(false)
const detailPresetHours = [1, 6, 24]
const detailCustom = ref('')

async function load() {
  error.value = ''
  try {
    const qs = new URLSearchParams({ page: String(page.value) })
    if (kind.value) qs.set('kind', kind.value)
    if (query.value.trim()) qs.set('q', query.value.trim())
    const d = await fetchJSON(`/api/admin/v1/access-logs?${qs}`)
    logs.value = d.logs || []
    total.value = d.total || 0
  } catch (e) { error.value = e.message }
}

async function loadDetail() {
  try {
    const d = await fetchJSON('/api/admin/v1/access-log/detail-mode')
    detailEnabled.value = !!d.enabled
    detailUntil.value = d.until || ''
    detailRemaining.value = d.seconds_remaining || 0
  } catch (e) { detailError.value = e.message }
}

// Post an absolute RFC3339 expiry and refresh the switch state.
async function setDetailUntil(rfc) {
  detailError.value = ''
  detailBusy.value = true
  try {
    await fetchJSON('/api/admin/v1/access-log/detail-mode', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ until: rfc }),
    })
    await loadDetail()
  } catch (e) { detailError.value = e.message } finally { detailBusy.value = false }
}

// A preset opens the window N hours from now; the backend rejects >24h, and
// the picker's largest preset matches that cap so the UI never offers a value
// it will refuse.
function applyPreset(h) {
  setDetailUntil(new Date(Date.now() + h * 3600 * 1000).toISOString())
}

// datetime-local yields a naive local string; Date() anchors it to the
// browser zone and toISOString() returns the absolute UTC RFC3339 the API wants.
function applyCustom() {
  if (!detailCustom.value) return
  const d = new Date(detailCustom.value)
  if (Number.isNaN(d.getTime())) { detailError.value = t('admin.accessDetailInvalid'); return }
  setDetailUntil(d.toISOString())
}

async function disableDetail() {
  detailError.value = ''
  detailBusy.value = true
  try {
    await fetchJSON('/api/admin/v1/access-log/detail-mode', { method: 'DELETE' })
    await loadDetail()
  } catch (e) { detailError.value = e.message } finally { detailBusy.value = false }
}

function search() { page.value = 1; load() }

function goPage(p) {
  if (p < 1 || (p - 1) * pageSize >= total.value || p === page.value) return
  page.value = p
  load()
}
const pageMax = () => Math.max(1, Math.ceil(total.value / pageSize))

// createdAt digits are the DB's wall-clock time (the driver tags them Z
// but carries no loc=); every other panel table shows these strings raw.
// Display the digits as-is instead of letting Date() re-interpret the
// (aspirational) zone — otherwise rows render +8h off on this deployment.
function fmtTime(s) {
  if (!s) return '—'
  const iso = String(s)
  const t = iso.indexOf('T')
  return (t >= 0 ? iso.slice(0, t) + ' ' + iso.slice(t + 1) : iso)
    .replace(/\.\d.*$/, '').replace(/[Zz]$|[+-]\d\d:\d\d$/, '')
}

// Outcome badge tone: ok green, miss muted, denied amber, fail/error red.
function outcomeClass(o) {
  return { ok: 'oc-ok', miss: 'oc-miss', denied: 'oc-warn' }[o] || 'oc-bad'
}

onMounted(() => { load(); loadDetail() })
</script>

<template>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('admin.accessTitle') }}</h2>
    <p class="muted" style="margin-top: -6px;">{{ t('admin.accessHint') }}</p>
    <section class="detail-mode">
      <h3 class="dm-title">{{ t('admin.accessDetailTitle') }}</h3>
      <p class="muted">{{ t('admin.accessDetailHint') }}</p>
      <p class="dm-state">
        <span class="oc" :class="detailEnabled ? 'oc-ok' : 'oc-miss'">
          {{ detailEnabled ? t('admin.accessDetailOn') : t('admin.accessDetailOff') }}
        </span>
        <span v-if="detailEnabled" class="muted">
          {{ t('admin.accessDetailExpires', { time: fmtTime(detailUntil) }) }}
          · {{ t('admin.accessDetailRemaining', { seconds: detailRemaining }) }}
        </span>
      </p>
      <div class="row dm-controls">
        <button v-for="h in detailPresetHours" :key="h" class="secondary"
                :disabled="detailBusy" @click="applyPreset(h)">
          {{ t('admin.accessDetailHours', { hours: h }) }}
        </button>
        <input v-model="detailCustom" type="datetime-local" :aria-label="t('admin.accessDetailCustom')" />
        <button class="secondary" :disabled="detailBusy || !detailCustom" @click="applyCustom">
          {{ t('admin.accessDetailApply') }}
        </button>
        <button v-if="detailEnabled" class="danger" :disabled="detailBusy" @click="disableDetail">
          {{ t('admin.accessDetailDisable') }}
        </button>
      </div>
      <div class="error" role="alert" v-if="detailError">{{ detailError }}</div>
    </section>
    <div class="row">
      <select v-model="kind" @change="search" style="max-width: 200px;">
        <option value="">{{ t('admin.accessKindAll') }}</option>
        <option v-for="k in KINDS" :key="k" :value="k">{{ t(`admin.kind.${k}`) }}</option>
      </select>
      <input v-model="query" :placeholder="t('admin.accessSearchPlaceholder')" style="max-width: 260px;"
             @keyup.enter="search" />
      <button class="secondary" @click="search">{{ t('admin.search') }}</button>
      <span class="muted">{{ t('admin.accessTotal', { total }) }}</span>
    </div>
    <div class="error" role="alert" v-if="error">{{ error }}</div>
    <div class="table-scroll" v-if="logs.length">
    <table class="logs-table" style="margin-top: 10px;">
      <tr>
        <th>{{ t('admin.colTime') }}</th><th>{{ t('admin.colKind') }}</th>
        <th>{{ t('admin.colOutcome') }}</th><th>{{ t('admin.colWho') }}</th>
        <th>IP</th><th>UA</th><th>{{ t('admin.colDetail') }}</th>
      </tr>
      <tr v-for="row in logs" :key="row.id">
        <td class="nowrap muted">{{ fmtTime(row.createdAt) }}</td>
        <td class="nowrap">{{ t(`admin.kind.${row.kind}`) }}</td>
        <td class="nowrap"><span class="oc" :class="outcomeClass(row.outcome)">{{ t(`admin.outcome.${row.outcome}`) }}</span></td>
        <td class="nowrap who">{{ row.who || '—' }} <span class="muted" v-if="row.uid">#{{ row.uid }}</span></td>
        <td class="nowrap"><code class="ip">{{ row.ip || '—' }}</code></td>
        <td class="ua" :title="row.ua || ''">{{ row.ua || '—' }}</td>
        <td class="detail" :title="row.detail || ''">{{ row.detail || '—' }}</td>
      </tr>
    </table>
    </div>
    <p class="muted" v-else-if="!error">{{ t('admin.accessEmpty') }}</p>
    <Pager :page="page" :pages="pageMax()" :total="total" @update:page="goPage" />
  </div>
</template>

<style scoped>
/* diagnostic-mode switch: a quiet bordered band above the filters; the state
   badge reuses the table's outcome tones so ON/OFF reads the same language. */
.detail-mode { border: 1px solid var(--line); border-radius: 10px; padding: 12px 14px; margin: 10px 0 4px; }
.dm-title { margin: 0 0 2px; font-size: var(--fs-m, 1rem); }
.dm-state { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; margin: 8px 0; }
.dm-controls { flex-wrap: wrap; gap: 8px; }
.dm-controls input[type="datetime-local"] { max-width: 220px; }
.logs-table { min-width: 960px; width: 100%; table-layout: fixed; }
/* fixed layout + every cell ellipsizes: nothing spills into a neighbour */
.logs-table td { overflow: hidden; text-overflow: ellipsis; }
.logs-table th:nth-child(1), .logs-table td:nth-child(1) { width: 160px; }
.logs-table th:nth-child(2), .logs-table td:nth-child(2) { width: 96px; }
.logs-table th:nth-child(3), .logs-table td:nth-child(3) { width: 62px; }
.logs-table th:nth-child(4), .logs-table td:nth-child(4) { width: 138px; }
.logs-table th:nth-child(5), .logs-table td:nth-child(5) { width: 112px; }
.logs-table th:nth-child(6), .logs-table td:nth-child(6) { width: 210px; }
/* col7 (detail) takes the remainder of the fixed layout */
.logs-table td.detail { font-family: var(--mono, monospace); font-size: var(--fs-xs); }
.nowrap { white-space: nowrap; }
.who { font-weight: 600; }
.ip { font-size: var(--fs-xs); }
.ua, .logs-table td.detail {
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}
.ua { font-size: var(--fs-xs); }
.oc { display: inline-block; padding: 1px 8px; border-radius: 999px; font-size: var(--fs-xs); border: 1px solid transparent; }
.oc-ok { color: var(--ok); border-color: color-mix(in srgb, var(--ok) 40%, transparent); }
.oc-miss { color: var(--muted); border-color: var(--line); }
.oc-warn { color: var(--gold); border-color: color-mix(in srgb, var(--gold) 40%, transparent); }
.oc-bad { color: var(--danger); border-color: color-mix(in srgb, var(--danger) 40%, transparent); }
</style>
