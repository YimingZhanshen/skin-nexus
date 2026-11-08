<script setup>
// Texture report moderation: list open reports, dismiss/resolve, or hide the
// texture. "Hide" strikes it — private, cleared from the public dedupe key, and
// barred from being republished by its owner until an admin lifts the verdict.
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'
import { confirmDialog } from '../../dialog.js'
import Pager from '../Pager.vue'
import EmptyState from '../EmptyState.vue'

const { t } = useI18n()
const reports = ref([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const pages = computed(() => Math.max(1, Math.ceil(total.value / pageSize)))
function turnTo(p) {
  if (p < 1 || p > pages.value || p === page.value) return
  page.value = p
  load()
}
const status = ref('open')
const loading = ref(false)
const busy = ref(0)
const error = ref('')
const ok = ref('')
let seq = 0

const reasons = {
  copyright: 'admin.reasonCopyright',
  sexual: 'admin.reasonSexual',
  violence: 'admin.reasonViolence',
  hate: 'admin.reasonHate',
  spam: 'admin.reasonSpam',
  other: 'admin.reasonOther',
}

async function load() {
  const my = ++seq
  loading.value = true
  error.value = ''
  try {
    const d = await fetchJSON(`/api/admin/v1/reports?status=${encodeURIComponent(status.value)}&page=${page.value}`)
    if (my !== seq) return
    reports.value = d.reports || []
    total.value = d.total || 0
  } catch (e) {
    if (my === seq) error.value = e.message
  } finally {
    if (my === seq) loading.value = false
  }
}

function setStatus(s) {
  status.value = s
  page.value = 1
  load()
}

async function act(r, action) {
  if (action === 'hide' && !(await confirmDialog(t('admin.reportsHideConfirm', { name: r.textureName || r.textureId }), { danger: true }))) return
  busy.value = r.id
  error.value = ''
  ok.value = ''
  try {
    await fetchJSON(`/api/admin/v1/reports/${r.id}/resolve`, {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ action }),
    })
    ok.value = t('admin.reportsHandled')
    await load()
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = 0
  }
}

function reasonText(r) { const k = reasons[r]; return k ? t(k) : r }

async function lift(r) {
  busy.value = r.id
  error.value = ''
  ok.value = ''
  try {
    await fetchJSON(`/api/admin/v1/textures/${r.textureId}/moderation`, {
      method: 'PUT', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ moderation: '' }),
    })
    ok.value = t('admin.reportsLifted')
    await load()
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = 0
  }
}

// The delete reason comes from the report itself: the tombstone then says why
// the texture went away, which is the whole point of keeping it.
function reportReason(r) {
  const text = r.detail ? `${r.reason}: ${r.detail}` : r.reason
  return [...text].slice(0, 500).join('')
}

function removeTexture(tid, force, reason) {
  const q = new URLSearchParams()
  if (force) q.set('force', '1')
  if (reason) q.set('reason', reason)
  return fetchJSON(`/api/admin/v1/textures/${tid}?${q}`, { method: 'DELETE' })
}

async function purge(r) {
  const name = r.textureName || ('#' + r.textureId)
  if (!(await confirmDialog(t('admin.reportsDeleteConfirm', { name }), { danger: true }))) return
  busy.value = r.id
  error.value = ''
  ok.value = ''
  try {
    try {
      await removeTexture(r.textureId, false, reportReason(r))
    } catch (e) {
      // 409 answers with what still points at the texture; that list is the
      // confirmation the admin actually needs before unbinding it.
      if (e.status !== 409 || !e.data || !Array.isArray(e.data.players)) throw e
      const detail = t('admin.reportsDeleteBlocked', {
        players: (e.data.players || []).join('、') || '—',
        closets: e.data.closets || 0,
        avatars: e.data.avatars || 0,
      })
      // the blockers as the heading, the consequence as the question
      if (!(await confirmDialog(t('admin.reportsDeleteForceConfirm'), { title: detail, danger: true }))) return
      await removeTexture(r.textureId, true, reportReason(r))
    }
    ok.value = t('admin.reportsDeleted')
    await load()
  } catch (e) {
    error.value = e.message
  } finally {
    busy.value = 0
  }
}

onMounted(load)
</script>

<template>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('admin.reportsTitle') }}</h2>
    <div class="row" style="gap: 8px; align-items: center; flex-wrap: wrap;">
      <button class="secondary" :class="{ active: status === 'open' }" @click="setStatus('open')">{{ t('admin.reportsOpen') }}</button>
      <button class="secondary" :class="{ active: status === 'all' }" @click="setStatus('all')">{{ t('admin.reportsAll') }}</button>
      <span class="muted">{{ t('admin.reportsTotal', { n: total }) }}</span>
    </div>
    <div class="error" role="alert" v-if="error">{{ error }}</div>
    <div class="success" role="status" v-if="ok">{{ ok }}</div>
    <p class="muted" v-if="loading">{{ t('common.loading') }}</p>
    <EmptyState v-else-if="!reports.length" :text="t('admin.reportsEmpty')" />
    <div class="table-scroll" v-else>
    <table>
      <caption class="sr-only">{{ t('admin.reportsTitle') }}</caption>
      <thead>
        <tr>
          <th scope="col">{{ t('admin.reportsTexture') }}</th>
          <th scope="col">{{ t('admin.reportsReport') }}</th>
          <th scope="col">{{ t('admin.reportsStatus') }}</th>
          <th scope="col">{{ t('admin.reportsActions') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in reports" :key="r.id">
          <td>
            <div class="report-tex">
              <a :href="'/skinlib/' + r.textureId" target="_blank" rel="noopener">
                <img v-if="r.textureType === 'skin'" :src="r.previewURL" alt="" loading="lazy" decoding="async" />
                <span v-else class="muted">#{{ r.textureId }}</span>
              </a>
              <div>
                <div>{{ r.textureName || ('#' + r.textureId) }}</div>
                <div class="muted">{{ t('admin.reportsUploader') }}: {{ r.uploader || '—' }} · {{ r.public ? t('admin.texPublic') : t('admin.texPrivate') }}</div>
                <div v-if="r.moderation" class="struck">{{ t('admin.reportsStruck') }}</div>
              </div>
            </div>
          </td>
          <td>
            <div>{{ reasonText(r.reason) }}</div>
            <div class="muted">{{ t('admin.reportsReporter') }}: {{ r.reporter || '—' }}</div>
            <div v-if="r.detail" class="report-detail">{{ r.detail }}</div>
          </td>
          <td>
            <div>{{ r.status }}</div>
            <div class="muted">{{ r.createdAt }}</div>
          </td>
          <td>
            <div class="report-actions">
              <a class="secondary btn-link" :href="'/skinlib/' + r.textureId" target="_blank" rel="noopener">{{ t('admin.reportsView') }}</a>
              <button class="secondary" :disabled="busy === r.id || r.status !== 'open'" @click="act(r, 'dismiss')">{{ t('admin.reportsDismiss') }}</button>
              <button class="secondary" :disabled="busy === r.id || r.status !== 'open'" @click="act(r, 'resolve')">{{ t('admin.reportsResolve') }}</button>
              <button :disabled="busy === r.id || r.status !== 'open'" @click="act(r, 'hide')">{{ t('admin.reportsHide') }}</button>
              <button v-if="r.moderation" class="secondary" :disabled="busy === r.id" @click="lift(r)">{{ t('admin.reportsLift') }}</button>
              <button class="danger-outline" :disabled="busy === r.id" @click="purge(r)">{{ t('admin.reportsDelete') }}</button>
            </div>
          </td>
        </tr>
      </tbody>
    </table>
    </div>
    <Pager :page="page" :pages="pages" :total="total" @update:page="turnTo" />
  </div>
</template>

<style scoped>
.report-tex { display: flex; gap: 10px; align-items: center; }
.report-tex img { width: 42px; height: 42px; object-fit: contain; image-rendering: pixelated; background: var(--bg); border-radius: 4px; }
.report-detail { margin-top: 4px; font-size: var(--fs-xs); color: var(--muted); white-space: pre-wrap; }
.struck { margin-top: 4px; font-size: var(--fs-xs); color: var(--danger); }
.report-actions { display: flex; gap: 6px; flex-wrap: wrap; }
.btn-link { display: inline-flex; align-items: center; padding: 6px 12px; text-decoration: none; }
button.active { border-color: var(--accent); color: var(--accent); }
</style>
