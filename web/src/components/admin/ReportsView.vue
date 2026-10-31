<script setup>
// Texture report moderation: list open reports, dismiss/resolve, or hide the
// texture. Hiding calls the same visibility path as the owner toggle, so the
// public dedupe key is cleared and the detail page becomes owner-only.
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'
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
  copyright: '侵权 / 无授权转载',
  sexual: '色情或擦边内容',
  violence: '暴力或血腥',
  hate: '仇恨或歧视',
  spam: '垃圾或误导信息',
  other: '其他',
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
  if (action === 'hide' && !window.confirm(t('admin.reportsHideConfirm', { name: r.textureName || r.textureId }))) return
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

function reasonText(r) { return reasons[r] || r }

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
    <div class="error" v-if="error">{{ error }}</div>
    <div class="success" v-if="ok">{{ ok }}</div>
    <p class="muted" v-if="loading">{{ t('common.loading') }}</p>
    <EmptyState v-else-if="!reports.length" :text="t('admin.reportsEmpty')" />
    <div class="table-scroll" v-else>
    <table>
      <thead>
        <tr>
          <th>{{ t('admin.reportsTexture') }}</th>
          <th>{{ t('admin.reportsReport') }}</th>
          <th>{{ t('admin.reportsStatus') }}</th>
          <th>{{ t('admin.reportsActions') }}</th>
        </tr>
      </thead>
      <tbody>
        <tr v-for="r in reports" :key="r.id">
          <td>
            <div class="report-tex">
              <a :href="'/skinlib/' + r.textureId" target="_blank" rel="noopener">
                <img v-if="r.textureType === 'skin'" :src="`/preview/${r.textureId}?v=5&small=1`" alt="" loading="lazy" decoding="async" />
                <span v-else class="muted">#{{ r.textureId }}</span>
              </a>
              <div>
                <div>{{ r.textureName || ('#' + r.textureId) }}</div>
                <div class="muted">{{ t('admin.reportsUploader') }}: {{ r.uploader || '—' }} · {{ r.public ? t('admin.reportsPublic') : t('admin.reportsPrivate') }}</div>
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
.report-actions { display: flex; gap: 6px; flex-wrap: wrap; }
.btn-link { display: inline-flex; align-items: center; padding: 6px 12px; text-decoration: none; }
button.active { border-color: var(--accent); color: var(--accent); }
</style>
