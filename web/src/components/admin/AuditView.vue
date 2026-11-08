<script setup>
// AuditView: who did what from the panel. The endpoint existed since the audit
// table landed but the panel never read it, so the only way to check an action
// trail was an X-Admin-Token call — which is also the only way to leave no trace
// of having looked.
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'
import Pager from '../Pager.vue'

const { t, te } = useI18n()

const rows = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 100
const loading = ref(false)
const error = ref('')
const q = ref('')
const action = ref('')
const actions = ref([])
let seq = 0
let timer = null

async function load() {
  const mine = ++seq
  loading.value = true
  error.value = ''
  try {
    const p = new URLSearchParams({ page: String(page.value) })
    if (q.value.trim()) p.set('q', q.value.trim())
    if (action.value) p.set('action', action.value)
    const d = await fetchJSON(`/api/admin/v1/audit?${p}`)
    if (mine !== seq) return
    rows.value = d.audit || []
    total.value = d.total || 0
    actions.value = d.actions || []
  } catch (e) {
    if (mine !== seq) return
    error.value = e.message
  } finally {
    if (mine === seq) loading.value = false
  }
}

// Search debounces like every other list here; a keystroke per full-table
// LIKE on an audit log that only grows would be a self-inflicted denial of
// service on the operator's own database.
function search() {
  clearTimeout(timer)
  timer = setTimeout(() => { page.value = 1; load() }, 300)
}
function setAction(v) { action.value = v; page.value = 1; load() }
function clearFilters() {
  q.value = ''; action.value = ''
  page.value = 1
  load()
}
const filtering = () => !!(q.value.trim() || action.value)

// The audit table stores machine names, one per write site in the server. Every
// name the current build can write has a label in both locales — rule 8 of
// check-locales reads the vocabulary back out of the Go source and proves it.
// A row older than this build has no label and stays as written: raw is
// unreadable, but `audit.action.seo_save` is unreadable and looks like a bug.
function actionLabel(name) {
  const key = `audit.action.${String(name || '').replace(/\./g, '__')}`
  return te(key) ? t(key) : name
}

function actorLabel(row) {
  if (row.actorName) return row.actorName
  // 0 is the X-Admin-Token path: no account stands behind it, and pretending
  // otherwise would blame a person for a script's work.
  if (!Number(row.actorUid)) return t('admin.auditSystem')
  return '#' + row.actorUid
}

function goPage(p) {
  if (p < 1 || (p - 1) * pageSize >= total.value || p === page.value) return
  page.value = p
  load()
}
const pageMax = () => Math.max(1, Math.ceil(total.value / pageSize))

// Same display rule as the other two time columns: the digits are the DB's wall
// clock, so letting Date() re-read the (aspirational) Z shifts every row.
function fmtTime(s) {
  if (!s) return '—'
  const iso = String(s)
  const t = iso.indexOf('T')
  return (t >= 0 ? iso.slice(0, t) + ' ' + iso.slice(t + 1) : iso)
    .replace(/\.\d.*$/, '').replace(/[Zz]$|[+-]\d\d:\d\d$/, '')
}

// detail is the JSON blob the writer chose; it is machine-named, so it is shown
// as written rather than guessed at.
function detail(row) {
  const raw = String(row.detail || '')
  if (!raw || raw === '{}') return ''
  try { return JSON.stringify(JSON.parse(raw)) } catch { return raw }
}

onMounted(load)
</script>

<template>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('admin.auditTitle') }}</h2>
    <p class="muted" style="margin-top: -6px;">{{ t('admin.auditHint') }}</p>
    <div class="row audit-bar">
      <button class="secondary" @click="load">{{ t('admin.reload') }}</button>
      <input v-model="q" :placeholder="t('admin.auditSearchPh')" :aria-label="t('admin.auditSearchPh')"
             style="max-width: 240px;" @input="search" />
      <select :value="action" :aria-label="t('admin.auditActionFilter')" style="max-width: 200px;"
              @change="setAction($event.target.value)">
        <option value="">{{ t('admin.auditActionAll') }}</option>
        <option v-for="a in actions" :key="a" :value="a" :title="a">{{ actionLabel(a) }}</option>
      </select>
      <button class="secondary" v-if="filtering()" @click="clearFilters">{{ t('dashboard.texClear') }}</button>
      <span class="muted">{{ t('admin.auditTotal', { total }) }}</span>
      <span class="muted" v-if="loading">{{ t('common.loading') }}</span>
    </div>
    <div class="error" role="alert" v-if="error">{{ error }}</div>
    <div class="table-scroll" v-if="rows.length">
      <table class="audit-table">
        <tr>
          <th>{{ t('admin.colTime') }}</th><th>{{ t('admin.colActor') }}</th>
          <th>{{ t('admin.colAction') }}</th><th>{{ t('admin.colDetail') }}</th>
        </tr>
        <tr v-for="row in rows" :key="row.id">
          <td class="nowrap muted">{{ fmtTime(row.createdAt) }}</td>
          <td class="nowrap muted" :title="row.actorUid ? ('#' + row.actorUid) : ''">{{ actorLabel(row) }}</td>
          <td class="nowrap" :title="row.action">{{ actionLabel(row.action) }}</td>
          <td class="detail" :title="detail(row)">{{ detail(row) || '—' }}</td>
        </tr>
      </table>
    </div>
    <p class="muted" v-else-if="!loading && !error">{{ t('admin.auditEmpty') }}</p>
    <Pager :page="page" :pages="pageMax()" :total="total" @update:page="goPage" />
  </div>
</template>

<style scoped>
.audit-table { min-width: 860px; width: 100%; table-layout: fixed; margin-top: 10px; }
.audit-table td { overflow: hidden; text-overflow: ellipsis; }
.audit-table th:nth-child(1), .audit-table td:nth-child(1) { width: 160px; }
.audit-table th:nth-child(2), .audit-table td:nth-child(2) { width: 84px; }
.audit-table th:nth-child(3), .audit-table td:nth-child(3) { width: 260px; }
.nowrap { white-space: nowrap; }
.mono { font-family: var(--mono, monospace); font-size: var(--fs-xs); }
.detail { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-size: var(--fs-xs); }
</style>
