<script setup>
// AccessLogsView (2026-10-26 观测面): web/launcher logins, Yggdrasil
// server traffic and Union traffic in one filterable page — the admin's
// "what is happening right now" view. Read-only; retention is server-side
// (90 days).
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

onMounted(load)
</script>

<template>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('admin.accessTitle') }}</h2>
    <p class="muted" style="margin-top: -6px;">{{ t('admin.accessHint') }}</p>
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
    <div class="error" v-if="error">{{ error }}</div>
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
    <p class="muted" v-else>{{ t('admin.accessEmpty') }}</p>
    <Pager :page="page" :pages="pageMax()" :total="total" @update:page="goPage" />
  </div>
</template>

<style scoped>
/* UA/detail are single-line ellipsis cells with hover title: a raw
   word-break let auto-layout starve the UA column to ~1 char per line
   (rows grew screen-tall). Fixed column budget = stable geometry. */
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
