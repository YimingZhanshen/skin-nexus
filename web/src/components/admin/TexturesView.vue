<script setup>
// TexturesView: the library as the site owner sees it. Before this view existed
// the material list was reachable only through X-Admin-Token, so「已下架」and
// 「还在库里」could not be told apart from the panel — the two columns that
// decide that (visibility and the moderation verdict) are the whole point.
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'
import Pager from '../Pager.vue'

const { t } = useI18n()

const rows = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 50
const loading = ref(false)
const error = ref('')
// Rows carry the id, not a snapshot of it: a re-render must not show a page that
// belongs to an older request.
let seq = 0

async function load() {
  const mine = ++seq
  loading.value = true
  error.value = ''
  try {
    const d = await fetchJSON(`/api/admin/v1/textures?page=${page.value}`)
    if (mine !== seq) return
    rows.value = d.textures || []
    total.value = d.total || 0
  } catch (e) {
    if (mine !== seq) return
    error.value = e.message
  } finally {
    if (mine === seq) loading.value = false
  }
}

function goPage(p) {
  if (p < 1 || (p - 1) * pageSize >= total.value || p === page.value) return
  page.value = p
  load()
}
const pageMax = () => Math.max(1, Math.ceil(total.value / pageSize))

// Same display rule as the access-log table: the driver stamps these Z but the
// digits are the DB's wall clock, so re-interpretating them shifts every row.
function fmtTime(s) {
  if (!s) return '—'
  const iso = String(s)
  const t = iso.indexOf('T')
  return (t >= 0 ? iso.slice(0, t) + ' ' + iso.slice(t + 1) : iso)
    .replace(/\.\d.*$/, '').replace(/[Zz]$|[+-]\d\d:\d\d$/, '')
}

function verdict(row) {
  if (row.moderation === 'violation') return { key: 'admin.texStruck', tone: 'oc-bad' }
  return row.public ? { key: 'admin.texPublic', tone: 'oc-ok' } : { key: 'admin.texPrivate', tone: 'oc-miss' }
}

onMounted(load)
</script>

<template>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('admin.texTitle') }}</h2>
    <p class="muted" style="margin-top: -6px;">{{ t('admin.texHint') }}</p>
    <div class="row">
      <button class="secondary" @click="load">{{ t('admin.load') }}</button>
      <span class="muted">{{ t('admin.texTotal', { total }) }}</span>
      <span class="muted" v-if="loading">{{ t('common.loading') }}</span>
    </div>
    <div class="error" role="alert" v-if="error">{{ error }}</div>
    <div class="table-scroll" v-if="rows.length">
      <table class="tex-table">
        <tr>
          <th>#</th><th>{{ t('admin.colTime') }}</th><th>{{ t('admin.colName') }}</th>
          <th>{{ t('admin.colType') }}</th><th>{{ t('admin.colUploader') }}</th>
          <th>{{ t('admin.colVisibility') }}</th><th>hash</th>
        </tr>
        <tr v-for="row in rows" :key="row.id">
          <td class="muted">{{ row.id }}</td>
          <td class="nowrap muted">{{ fmtTime(row.uploadAt) }}</td>
          <td class="name" :title="row.name || ''">{{ row.name || '—' }}</td>
          <td class="nowrap">{{ row.type }}<span class="muted" v-if="row.model"> · {{ row.model }}</span></td>
          <td class="nowrap muted">#{{ row.uid }}</td>
          <td class="nowrap"><span class="oc" :class="verdict(row).tone">{{ t(verdict(row).key) }}</span></td>
          <td><code class="hash">{{ row.hash }}</code></td>
        </tr>
      </table>
    </div>
    <p class="muted" v-else-if="!loading && !error">{{ t('admin.texEmpty') }}</p>
    <Pager :page="page" :pages="pageMax()" :total="total" @update:page="goPage" />
  </div>
</template>

<style scoped>
.tex-table { min-width: 900px; width: 100%; table-layout: fixed; margin-top: 10px; }
.tex-table td { overflow: hidden; text-overflow: ellipsis; }
.tex-table th:nth-child(1), .tex-table td:nth-child(1) { width: 62px; }
.tex-table th:nth-child(2), .tex-table td:nth-child(2) { width: 160px; }
.tex-table th:nth-child(4), .tex-table td:nth-child(4) { width: 120px; }
.tex-table th:nth-child(5), .tex-table td:nth-child(5) { width: 78px; }
.tex-table th:nth-child(6), .tex-table td:nth-child(6) { width: 104px; }
.name { font-weight: 600; }
.nowrap { white-space: nowrap; }
.hash { font-size: var(--fs-xs); }
.oc { display: inline-block; padding: 1px 8px; border-radius: 999px; font-size: var(--fs-xs); border: 1px solid transparent; }
.oc-ok { color: var(--ok); border-color: color-mix(in srgb, var(--ok) 40%, transparent); }
.oc-miss { color: var(--muted); border-color: var(--line); }
.oc-bad { color: var(--danger); border-color: color-mix(in srgb, var(--danger) 40%, transparent); }
</style>
