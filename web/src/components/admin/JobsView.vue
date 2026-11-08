<script setup>
// JobsView: the persistent plugin job queue (importer batches, scheduled work).
// The queue has been running since P3 with no panel surface at all, so an
// operator could only see it by calling the API with X-Admin-Token — including
// the case where a batch stops moving and nobody knows it stopped.
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'
import { confirmDialog } from '../../dialog.js'
import Pager from '../Pager.vue'

const { t } = useI18n()

const rows = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const state = ref('')
const plugin = ref('')
const loading = ref(false)
const error = ref('')
const ok = ref('')
const unavailable = ref(false)
// One control action in flight: a second 取消 on the same job would only be
// refused by the runner, and a double click on 恢复 reads as two resumes.
const busy = ref('')

const STATES = ['queued', 'running', 'paused', 'cancelling', 'cancelled', 'failed', 'done']

async function load() {
  loading.value = true
  error.value = ''
  unavailable.value = false
  try {
    const qs = new URLSearchParams({ page: String(page.value), perPage: String(pageSize) })
    if (state.value) qs.set('state', state.value)
    if (plugin.value.trim()) qs.set('plugin', plugin.value.trim())
    const d = await fetchJSON(`/api/admin/v1/jobs?${qs}`)
    rows.value = d.jobs || []
    total.value = d.total || 0
  } catch (e) {
    // The routes are mounted only when the queue is on, so 404 means "this site
    // has no job runner" — a fact about the deployment, not a failed request.
    if (e.status === 404) unavailable.value = true
    else error.value = e.message
  } finally {
    loading.value = false
  }
}

function search() { page.value = 1; load() }

function goPage(p) {
  if (p < 1 || (p - 1) * pageSize >= total.value || p === page.value) return
  page.value = p
  load()
}
const pageMax = () => Math.max(1, Math.ceil(total.value / pageSize))

// Cancelling and resuming are the whole reason this page exists: without them
// the queue can be watched stopping but not nudged. Each is confirmed because
// both change what runs next on a shared machine.
async function act(row, kind) {
  if (busy.value) return
  const label = row.handler + ' #' + row.id.slice(0, 6)
  if (!(await confirmDialog(t(kind === 'cancel' ? 'admin.jobCancelConfirm' : 'admin.jobResumeConfirm', { job: label })))) return
  busy.value = row.id
  error.value = ''
  ok.value = ''
  try {
    await fetchJSON(`/api/admin/v1/jobs/${encodeURIComponent(row.id)}/${kind}`, { method: 'POST' })
    ok.value = t(kind === 'cancel' ? 'admin.jobCancelSent' : 'admin.jobResumed')
    await load()
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = ''
  }
}

const canCancel = (s) => ['queued', 'running', 'paused'].includes(s)
// The set the server answers for, read off jobs/store.go Resume — its WHERE
// clause takes cancelled/cancelling too. The button used to hide for exactly
// those two, so a cancelled job could be seen but never restarted from the panel.
const canResume = (s) => ['paused', 'failed', 'cancelled', 'cancelling'].includes(s)

function stateTone(s) {
  return { done: 'oc-ok', failed: 'oc-bad', cancelled: 'oc-miss', running: 'oc-warn' }[s] || 'oc-miss'
}

onMounted(load)
</script>

<template>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('admin.jobsTitle') }}</h2>
    <p class="muted" style="margin-top: -6px;">{{ t('admin.jobsHint') }}</p>
    <div class="row">
      <select v-model="state" @change="search" style="max-width: 180px;">
        <option value="">{{ t('admin.jobsStateAll') }}</option>
        <option v-for="s in STATES" :key="s" :value="s">{{ t(`admin.jobState.${s}`) }}</option>
      </select>
      <input v-model="plugin" :placeholder="t('admin.jobsPluginPlaceholder')" style="max-width: 220px;"
             @keyup.enter="search" />
      <button class="secondary" @click="search">{{ t('admin.search') }}</button>
      <span class="muted">{{ t('admin.jobsTotal', { total }) }}</span>
      <span class="muted" v-if="loading">{{ t('common.loading') }}</span>
    </div>
    <div class="error" role="alert" v-if="error">{{ error }}</div>
    <div class="success" role="status" v-if="ok">{{ ok }}</div>
    <p class="muted" v-if="unavailable">{{ t('admin.jobsModuleOff') }}</p>
    <div class="table-scroll" v-else-if="rows.length">
      <table class="job-table">
        <caption class="sr-only">{{ t('admin.jobsTitle') }}</caption>
        <tr>
          <th scope="col">{{ t('admin.colTime') }}</th><th scope="col">{{ t('admin.colPlugin') }}</th>
          <th scope="col">{{ t('admin.colState') }}</th><th scope="col">{{ t('admin.colProgress') }}</th>
          <th scope="col">{{ t('admin.colAttempts') }}</th><th scope="col">{{ t('admin.colMessage') }}</th>
          <th scope="col">{{ t('admin.colJob') }}</th><th scope="col">{{ t('admin.colActions') }}</th>
        </tr>
        <tr v-for="row in rows" :key="row.id">
          <td class="nowrap muted">{{ row.updatedAt ? row.updatedAt.slice(0, 16).replace('T', ' ') : '—' }}</td>
          <td class="nowrap mono">{{ row.plugin }}</td>
          <td class="nowrap"><span class="oc" :class="stateTone(row.state)">{{ t(`admin.jobState.${row.state}`) }}</span></td>
          <td class="nowrap">{{ row.progress }}%</td>
          <td class="nowrap muted">{{ row.attempts }}/{{ row.maxAttempts }}<span v-if="row.reclaims"> ·↺{{ row.reclaims }}</span></td>
          <td class="msg" :title="row.lastError || row.message || ''">{{ row.lastError || row.message || '—' }}</td>
          <td class="nowrap muted" :title="row.id">{{ row.handler }} <span class="muted">#{{ row.id.slice(0, 6) }}</span></td>
          <td class="nowrap">
            <button class="secondary" v-if="canCancel(row.state)" :disabled="busy === row.id"
                    @click="act(row, 'cancel')">{{ t('admin.jobCancel') }}</button>
            <button class="secondary" v-if="canResume(row.state)" :disabled="busy === row.id"
                    @click="act(row, 'resume')">{{ t('admin.jobResume') }}</button>
            <span class="muted" v-if="!canCancel(row.state) &amp;&amp; !canResume(row.state)">—</span>
          </td>
        </tr>
      </table>
    </div>
    <p class="muted" v-else-if="!loading && !error">{{ t('admin.jobsEmpty') }}</p>
    <Pager :page="page" :pages="pageMax()" :total="total" @update:page="goPage" />
  </div>
</template>

<style scoped>
.job-table { min-width: 1080px; width: 100%; table-layout: fixed; margin-top: 10px; }
.job-table td { overflow: hidden; text-overflow: ellipsis; }
.job-table th:nth-child(1), .job-table td:nth-child(1) { width: 132px; }
.job-table th:nth-child(2), .job-table td:nth-child(2) { width: 150px; }
.job-table th:nth-child(3), .job-table td:nth-child(3) { width: 92px; }
.job-table th:nth-child(4), .job-table td:nth-child(4) { width: 56px; }
.job-table th:nth-child(5), .job-table td:nth-child(5) { width: 82px; }
.job-table th:nth-child(7), .job-table td:nth-child(7) { width: 170px; }
.job-table th:nth-child(8), .job-table td:nth-child(8) { width: 150px; }
.nowrap { white-space: nowrap; }
.mono { font-family: var(--mono, monospace); font-size: var(--fs-xs); }
.msg { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
.oc { display: inline-block; padding: 1px 8px; border-radius: 999px; font-size: var(--fs-xs); border: 1px solid transparent; }
.oc-ok { color: var(--ok); border-color: color-mix(in srgb, var(--ok) 40%, transparent); }
.oc-miss { color: var(--muted); border-color: var(--line); }
.oc-warn { color: var(--gold); border-color: color-mix(in srgb, var(--gold) 40%, transparent); }
.oc-bad { color: var(--danger); border-color: color-mix(in srgb, var(--danger) 40%, transparent); }
</style>
