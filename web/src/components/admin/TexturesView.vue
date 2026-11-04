<script setup>
// TexturesView: the library as the site owner sees it. Before this view existed
// the material list was reachable only through X-Admin-Token, so「已下架」and
// 「还在库里」could not be told apart from the panel — and when they could be
// told apart, nothing could be done about it: the list was read-only, with no
// way to find a texture by name, uploader or verdict, and no action but paging
// past it. The three columns that decide a texture's fate (uploader, visibility,
// moderation) are now filters, and the verdict itself is one click away.
import { ref, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'
import { confirmDialog, promptDialog } from '../../dialog.js'
import Pager from '../Pager.vue'

const { t } = useI18n()

const rows = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 50
const loading = ref(false)
const error = ref('')
const ok = ref('')
const q = ref('')
const typ = ref('')
const state = ref('')
// The row being acted on, so one button cannot be double-fired.
const busy = ref(0)
const copied = ref('')
// Rows carry the id, not a snapshot of it: a re-render must not show a page that
// belongs to an older request.
let seq = 0
let timer = null

function url() {
  const p = new URLSearchParams({ page: String(page.value) })
  if (q.value.trim()) p.set('q', q.value.trim())
  if (typ.value) p.set('type', typ.value)
  if (state.value) p.set('state', state.value)
  return `/api/admin/v1/textures?${p}`
}

async function load() {
  const mine = ++seq
  loading.value = true
  error.value = ''
  try {
    const d = await fetchJSON(url())
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

function search() {
  clearTimeout(timer)
  timer = setTimeout(() => { page.value = 1; load() }, 300)
}
function setTyp(v) { typ.value = v; page.value = 1; load() }
function setState(v) { state.value = v; page.value = 1; load() }
function clearFilters() {
  q.value = ''; typ.value = ''; state.value = ''
  page.value = 1
  load()
}
const filtering = () => !!(q.value.trim() || typ.value || state.value)

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
  const i = iso.indexOf('T')
  return (i >= 0 ? iso.slice(0, i) + ' ' + iso.slice(i + 1) : iso)
    .replace(/\.\d.*$/, '').replace(/[Zz]$|[+-]\d\d:\d\d$/, '')
}

function verdict(row) {
  if (row.moderation === 'violation') return { key: 'admin.texStruck', tone: 'oc-bad' }
  return row.public ? { key: 'admin.texPublic', tone: 'oc-ok' } : { key: 'admin.texPrivate', tone: 'oc-miss' }
}

async function copyHash(row) {
  try {
    await navigator.clipboard.writeText(row.hash)
    copied.value = String(row.id)
    error.value = ''
  } catch {
    // A refused clipboard must say so where the operator is looking: the cell can
    // only show back the hash it just failed to copy, and silence reads as success.
    copied.value = ''
    error.value = t('dashboard.copyFail')
  }
  setTimeout(() => { copied.value = '' }, 2000)
}

// Every mutation reloads the list: the verdict column is the operator's proof
// that the click landed, and a hand-patched row would let a failed reload look
// like a success.
async function act(row, fn, doneKey) {
  busy.value = row.id
  error.value = ''
  ok.value = ''
  try {
    await fn()
    ok.value = t(doneKey)
    await load()
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = 0
  }
}

async function strike(row) {
  const name = row.name || ('#' + row.id)
  if (!(await confirmDialog(t('admin.reportsHideConfirm', { name }), { danger: true }))) return
  await act(row, () => fetchJSON(`/api/admin/v1/textures/${row.id}/strike`, { method: 'POST' }), 'admin.reportsStruck')
}

async function lift(row) {
  await act(row, () => fetchJSON(`/api/admin/v1/textures/${row.id}/moderation`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ moderation: '' }),
  }), 'admin.reportsLifted')
}

async function purge(row) {
  const name = row.name || ('#' + row.id)
  if (!(await confirmDialog(t('admin.reportsDeleteConfirm', { name }), { danger: true }))) return
  // The reason is what the tombstone will say. It may be left blank, but
  // cancelling the prompt means "do not delete" — not "delete with no note".
  const reason = await promptDialog(t('admin.texReasonPrompt'), '')
  if (reason === null) return
  await act(row, async () => {
    const send = (force) => {
      const p = new URLSearchParams()
      if (force) p.set('force', '1')
      if (reason.trim()) p.set('reason', reason.trim().slice(0, 500))
      return fetchJSON(`/api/admin/v1/textures/${row.id}?${p}`, { method: 'DELETE' })
    }
    try {
      await send(false)
    } catch (e) {
      // 409 answers with what still points at the texture; that list is the
      // confirmation the operator actually needs before unbinding it.
      if (e.status !== 409 || !e.data || !Array.isArray(e.data.players)) throw e
      const detail = t('admin.reportsDeleteBlocked', {
        players: (e.data.players || []).join('、') || '—',
        closets: e.data.closets || 0,
        avatars: e.data.avatars || 0,
      })
      if (!(await confirmDialog(t('admin.reportsDeleteForceConfirm'), { title: detail, danger: true }))) return
      await send(true)
    }
  }, 'admin.reportsDeleted')
}

onMounted(load)
</script>

<template>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('admin.texTitle') }}</h2>
    <p class="muted" style="margin-top: -6px;">{{ t('admin.texHint') }}</p>
    <div class="row tex-bar">
      <button class="secondary" @click="load">{{ t('admin.reload') }}</button>
      <input v-model="q" :placeholder="t('admin.texSearchPh')" :aria-label="t('admin.texSearchPh')"
             style="max-width: 240px;" @input="search" />
      <select :value="typ" :aria-label="t('dashboard.texTypeLabel')" @change="setTyp($event.target.value)">
        <option value="">{{ t('admin.accessKindAll') }}</option>
        <option value="skin">{{ t('dashboard.colSkin') }}</option>
        <option value="cape">{{ t('dashboard.colCape') }}</option>
      </select>
      <select :value="state" :aria-label="t('admin.texStateFilter')" @change="setState($event.target.value)">
        <option value="">{{ t('admin.jobsStateAll') }}</option>
        <option value="public">{{ t('admin.texPublic') }}</option>
        <option value="private">{{ t('admin.texPrivate') }}</option>
        <option value="struck">{{ t('admin.texStruck') }}</option>
      </select>
      <button class="secondary" v-if="filtering()" @click="clearFilters">{{ t('dashboard.texClear') }}</button>
      <span class="muted">{{ t('admin.texTotal', { total }) }}</span>
      <span class="muted" v-if="loading">{{ t('common.loading') }}</span>
    </div>
    <div class="error" role="alert" v-if="error">{{ error }}</div>
    <div class="success" role="status" v-if="ok">{{ ok }}</div>
    <div class="table-scroll" tabindex="0" :aria-label="t('admin.texTitle')" v-if="rows.length">
      <table class="tex-table">
        <tr>
          <th>#</th><th></th><th>{{ t('admin.colTime') }}</th><th>{{ t('admin.colName') }}</th>
          <th>{{ t('admin.colType') }}</th><th>{{ t('admin.colUploader') }}</th>
          <th>{{ t('admin.colVisibility') }}</th><th>{{ t('dashboard.colHash') }}</th><th>{{ t('admin.colActions') }}</th>
        </tr>
        <tr v-for="row in rows" :key="row.id">
          <td class="muted">{{ row.id }}</td>
          <td class="tex-thumb">
            <img v-if="row.previewURL" :src="row.previewURL" alt="" loading="lazy" decoding="async" />
            <span class="muted" v-else>—</span>
          </td>
          <td class="nowrap muted">{{ fmtTime(row.uploadAt) }}</td>
          <td class="name" :title="row.name || ''">
            <a class="tex-open" :href="'/skinlib/' + row.id" target="_blank" rel="noopener">{{ row.name || '—' }}</a>
          </td>
          <td class="nowrap">{{ row.type }}<span class="muted" v-if="row.model"> · {{ row.model }}</span></td>
          <td class="nowrap">
            <span :title="'#' + row.uid">{{ row.nickname || ('#' + row.uid) }}</span>
          </td>
          <td class="nowrap"><span class="oc" :class="verdict(row).tone">{{ t(verdict(row).key) }}</span></td>
          <td class="nowrap">
            <button class="hash-btn" :class="{ copied: copied === String(row.id) }"
                    :title="t('admin.texCopyHint')" @click="copyHash(row)">
              <code class="hash">{{ copied === String(row.id) ? t('dashboard.copied') : row.hash.slice(0, 12) + '…' }}</code>
            </button>
          </td>
          <td class="nowrap tex-act">
            <button class="secondary" v-if="row.moderation !== 'violation'"
                    :disabled="busy === row.id" @click="strike(row)">{{ t('admin.reportsHide') }}</button>
            <button class="secondary" v-else :disabled="busy === row.id" @click="lift(row)">{{ t('admin.reportsLift') }}</button>
            <button class="secondary danger-outline" :disabled="busy === row.id" @click="purge(row)">{{ t('admin.reportsDelete') }}</button>
          </td>
        </tr>
      </table>
    </div>
    <p class="muted" v-else-if="!loading && !error">{{ filtering() ? t('skinlib.empty') : t('admin.texEmpty') }}</p>
    <Pager :page="page" :pages="pageMax()" :total="total" @update:page="goPage" />
  </div>
</template>

<style scoped>
.tex-bar { flex-wrap: wrap; gap: 8px; align-items: center; }
.tex-bar select { max-width: 150px; }
.tex-table { min-width: 1180px; width: 100%; table-layout: fixed; margin-top: 10px; }
.tex-table td { overflow: hidden; text-overflow: ellipsis; }
.tex-table th:nth-child(1), .tex-table td:nth-child(1) { width: 52px; }
.tex-table th:nth-child(2), .tex-table td:nth-child(2) { width: 56px; }
.tex-table th:nth-child(3), .tex-table td:nth-child(3) { width: 150px; }
.tex-table th:nth-child(5), .tex-table td:nth-child(5) { width: 110px; }
.tex-table th:nth-child(6), .tex-table td:nth-child(6) { width: 120px; }
.tex-table th:nth-child(7), .tex-table td:nth-child(7) { width: 100px; }
.tex-table th:nth-child(9), .tex-table td:nth-child(9) { width: 190px; }
.tex-thumb img { height: 44px; image-rendering: pixelated; border-radius: 4px; vertical-align: middle; }
.name { font-weight: 600; }
.nowrap { white-space: nowrap; }
.tex-open { color: inherit; text-decoration: none; }
.tex-open:hover { text-decoration: underline; }
.hash { font-size: var(--fs-xs); }
.hash-btn { border: none; background: transparent; color: var(--muted); cursor: pointer; padding: 2px 4px; min-height: 28px; }
.hash-btn:hover { color: var(--text); box-shadow: none; transform: none; background: transparent; }
.hash-btn.copied { color: var(--ok); }
.tex-act button { margin-right: 6px; }
.oc { display: inline-block; padding: 1px 8px; border-radius: 999px; font-size: var(--fs-xs); border: 1px solid transparent; }
.oc-ok { color: var(--ok); border-color: color-mix(in srgb, var(--ok) 40%, transparent); }
.oc-miss { color: var(--muted); border-color: var(--line); }
.oc-bad { color: var(--danger); border-color: color-mix(in srgb, var(--danger) 40%, transparent); }
</style>
