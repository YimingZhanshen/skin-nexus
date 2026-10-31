<script setup>
// UnionView: MUA Union module status + rotation resume + ops buttons (B5.3)
// + union blacklist proxy (B5.4). The module-disabled case shows an empty
// state. Release/Delete are irreversible-ish — both double-confirm, delete
// is permanent (hub erases the record).
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'

const { t } = useI18n()
const unionStatus = ref(null)
const error = ref('')
const loadFailed = ref(false)
const opResult = ref('')
const opBusy = ref(false)

// blacklist
const blRows = ref([])
const blTotal = ref(0)
const blPage = ref(1)
// hub 是 Laravel 分页（per_page 由 hub 定，不固定）——用上游权威的
// last_page，缺失时退化为 total/per_page 推算。
const blTotalPages = ref(1)
const blQ = ref('')
const blEmail = ref('')
const blReason = ref('')

async function load() {
  error.value = ''
  loadFailed.value = false
  // 404 = 模块关闭（预期空态）；其余错误必须可见且可重试，
  // 不能把「加载失败」伪装成「联盟功能未启用」（2026-10-27 逻辑修复）。
  try {
    unionStatus.value = await fetchJSON('/api/admin/v1/union/status')
  } catch (e) {
    unionStatus.value = null
    if (e.status === 404) return
    loadFailed.value = true
    error.value = e.message
  }
}

async function resumeRotation() {
  if (!confirm(t('admin.unionResumeConfirm'))) return
  try {
    await fetchJSON('/api/admin/v1/union/rotation/resume', { method: 'POST' })
    await load()
  } catch (e) { error.value = e.message }
}

async function runOp(name, path, confirmMsg) {
  if (confirmMsg && !confirm(confirmMsg)) return
  opBusy.value = true
  opResult.value = ''
  error.value = ''
  try {
    const d = await fetchJSON(path, { method: 'POST' })
    opResult.value = t('admin.unionOpDone', { name: t(name), detail: JSON.stringify(d).slice(0, 400) })
    await load()
  } catch (e) {
    error.value = e.message
  } finally {
    opBusy.value = false
  }
}

// ---- 自助诊断 (1:1 with legacy yggdrasil-connect triggerDiagnose) ----
// Three states (idle → running → result) and the same nine result rows the
// legacy modal renders, plus an exported standalone HTML report.
const diagOpen = ref(false)
const diagLoading = ref(false)
const diag = ref(null) // {status, data, key_fingerprint, ...}

function openDiagnose() {
  diagOpen.value = true
  diag.value = null
}

function closeDiagnose() {
  diagOpen.value = false
  diag.value = null
}

async function triggerDiagnose() {
  diagLoading.value = true
  diag.value = null
  try {
    // Non-2xx / transport failures still come back as {status:'error',data}
    // so one table renders every outcome — mirror of legacy triggerDiagnose.
    diag.value = await fetchJSON('/api/admin/v1/union/ops/diagnose', { method: 'POST' })
  } catch (e) {
    diag.value = { status: 'error', data: { exception: e.message } }
  } finally {
    diagLoading.value = false
  }
}

function diagRow(k, v) { return { k, v } }

const diagRows = computed(() => {
  const d = diag.value
  if (!d) return []
  const data = d.data || {}
  const rows = []
  // 1. 皮肤站→Union  2. Union→皮肤站 (legacy table order, verbatim)
  rows.push(diagRow(t('admin.diagSiteToUnion'), d.status === 'ok' ? 'ok' : 'fail'))
  if (d.status === 'ok') {
    rows.push(diagRow(t('admin.diagUnionToSite'), data.healthy ? 'ok' : 'fail'))
    if (data.healthy && typeof data.delay === 'number') {
      rows.push(diagRow(t('admin.diagDelay'), `${(data.delay * 1000).toFixed(2)} ms`))
    }
    if (data.timestamp) rows.push(diagRow(t('admin.diagTimestamp'), String(data.timestamp)))
  }
  if (data.tls_handshake) rows.push({ k: t('admin.diagTLS'), area: String(data.tls_handshake) })
  if (data.exception) rows.push({ k: t('admin.diagException'), area: String(data.exception) })
  if (data.status_code) rows.push(diagRow(t('admin.diagStatusCode'), String(data.status_code)))
  if (data.headers) rows.push({ k: t('admin.diagHeaders'), area: JSON.stringify(data.headers, null, 2) })
  if (data.body) rows.push({ k: t('admin.diagBody'), area: String(data.body) })
  return rows
})

function exportDiagnose() {
  const rows = diagRows.value.map((r) => {
    const val = r.area
      ? `<textarea readonly rows="8" style="width:100%;box-sizing:border-box;">${String(r.area).replace(/&/g, '&amp;').replace(/</g, '&lt;')}</textarea>`
      : r.v === 'ok' ? '<span style="color:#198754;font-weight:600;">✔ 成功</span>'
      : r.v === 'fail' ? '<span style="color:#dc3545;font-weight:600;">✘ 失败</span>'
      : String(r.v).replace(/&/g, '&amp;').replace(/</g, '&lt;')
    return `<tr><th style="text-align:left;vertical-align:top;width:20%;padding:6px 10px;">${r.k}</th><td style="padding:6px 10px;">${val}</td></tr>`
  }).join('')
  const meta = diag.value
    ? `<p>key_fingerprint: ${diag.value.key_fingerprint || '—'} · private_key_version: ${diag.value.private_key_version || '—'} · server_list_version: ${diag.value.server_list_version || '—'}</p>`
    : ''
  const html = `<!doctype html><html><head><meta charset="utf-8"><title>${t('admin.diagExportTitle')}</title></head><body><h2>${t('admin.diagExportTitle')}</h2><p>${new Date().toLocaleString()}</p>${meta}<table style="border-collapse:collapse;width:100%;">${rows}</table></body></html>`
  const url = URL.createObjectURL(new Blob([html], { type: 'text/html' }))
  const a = document.createElement('a')
  a.href = url
  a.download = `诊断报告-${Math.round(Date.now() / 1000)}.html`
  a.click()
  URL.revokeObjectURL(url)
}

async function loadBlacklist() {
  error.value = ''
  try {
    const d = await fetchJSON(`/api/admin/v1/union/blacklist?q=${encodeURIComponent(blQ.value)}&page=${blPage.value}`)
    // hub returns Laravel pagination JSON; tolerate both shaped answers
    blRows.value = d.data || d.items || []
    blTotal.value = d.total || (d.data ? d.data.length : 0)
    // per_page is hub-owned (not fixed) — trust upstream last_page first
    const per = d.per_page || (d.data && d.data.length) || 1
    blTotalPages.value = d.last_page || Math.max(1, Math.ceil((blTotal.value || 0) / per))
  } catch (e) {
    blRows.value = []
    error.value = e.message
  }
}

async function blAdd() {
  if (!blEmail.value.trim()) return
  error.value = ''
  try {
    await fetchJSON('/api/admin/v1/union/blacklist', {
      method: 'POST',
      body: JSON.stringify({ email: blEmail.value.trim(), reason: blReason.value.trim() }),
    })
    blEmail.value = ''
    blReason.value = ''
    await loadBlacklist()
  } catch (e) { error.value = e.message }
}

async function blRelease(row) {
  if (!confirm(t('admin.unionBlReleaseConfirm', { email: row.email || row.id }))) return
  error.value = ''
  try {
    await fetchJSON(`/api/admin/v1/union/blacklist/${encodeURIComponent(row.id)}/invalidate`, { method: 'PUT' })
    await loadBlacklist()
  } catch (e) { error.value = e.message }
}

async function blDelete(row) {
  if (!confirm(t('admin.unionBlDeleteConfirm1', { email: row.email || row.id }))) return
  if (!confirm(t('admin.unionBlDeleteConfirm2'))) return
  error.value = ''
  try {
    await fetchJSON(`/api/admin/v1/union/blacklist/${encodeURIComponent(row.id)}`, { method: 'DELETE' })
    await loadBlacklist()
  } catch (e) { error.value = e.message }
}

onMounted(async () => {
  await load()
  if (unionStatus.value) await loadBlacklist()
})
</script>

<template>
  <div>
    <div class="card" v-if="unionStatus">
      <h2 style="margin-top: 0;">{{ t('admin.unionTitle') }}</h2>
      <div class="row">
        <span class="badge">{{ t('admin.unionMode') }} {{ unionStatus.mode }}</span>
        <span class="badge">{{ t('admin.unionKeyFp') }} {{ unionStatus.key_fingerprint || '—' }}</span>
        <span class="badge">{{ t('admin.unionKeyVersion') }} {{ unionStatus.private_key_version || '—' }}</span>
        <span class="badge">{{ t('admin.unionServerListVersion', { version: unionStatus.server_list_version || '—' }) }}</span>
        <span class="badge">{{ t('admin.unionQueryEmail') }} {{ unionStatus.queryemail_mode }}</span>
        <span class="badge" :class="{ danger: unionStatus.rotation_suspended }">
          {{ unionStatus.rotation_suspended ? t('admin.unionRotationSuspended') : t('admin.unionRotationOk') }}
        </span>
      </div>
      <div class="row" style="margin-top: 12px;" v-if="unionStatus.rotation_suspended">
        <button class="secondary" @click="resumeRotation">{{ t('admin.unionResumeRotation') }}</button>
      </div>

      <!-- B5.3 ops buttons -->
      <div class="row" style="margin-top: 12px;">
        <button class="secondary" :disabled="opBusy" @click="runOp('admin.unionOpServerList', '/api/admin/v1/union/ops/serverlist')">
          {{ t('admin.unionOpServerList') }}
        </button>
        <button class="secondary" :disabled="opBusy" @click="runOp('admin.unionOpKeyUpdate', '/api/admin/v1/union/ops/key-update', t('admin.unionOpKeyUpdateConfirm'))">
          {{ t('admin.unionOpKeyUpdate') }}
        </button>
        <button class="secondary" :disabled="opBusy" @click="runOp('admin.unionOpRosterSync', '/api/admin/v1/union/ops/roster-sync')">
          {{ t('admin.unionOpRosterSync') }}
        </button>
        <button class="secondary" :disabled="opBusy" @click="openDiagnose">
          {{ t('admin.unionOpDiagnose') }}
        </button>
      </div>
      <div class="success" v-if="opResult">{{ opResult }}</div>

      <div class="muted" style="margin-top: 12px; font-size: 12px;">
        {{ t('admin.unionTrustNotice') }}
      </div>
      <div class="error" v-if="error">{{ error }}</div>
    </div>
    <div class="card" v-else-if="loadFailed">
      <div class="error">{{ error }}</div>
      <button class="secondary" style="margin-top: 12px;" @click="load">{{ t('common.retry') }}</button>
    </div>
    <div class="card" v-else>
      <h2 style="margin-top: 0;">{{ t('admin.unionTitle') }}</h2>
      <div class="muted">{{ t('admin.unionDisabled') }}</div>
    </div>

    <!-- B5.4 blacklist -->
    <div class="card" v-if="unionStatus">
      <h2 style="margin-top: 0;">{{ t('admin.unionBlTitle') }}</h2>
      <div class="row">
        <input v-model="blQ" :placeholder="t('admin.unionBlSearch')" style="max-width: 220px;" @keyup.enter="blPage = 1; loadBlacklist()" />
        <button class="secondary" @click="blPage = 1; loadBlacklist()">{{ t('admin.unionBlSearchBtn') }}</button>
      </div>
      <table v-if="blRows.length" class="bl-table" style="margin-top: 12px;">
        <tr>
          <th>ID</th><th>{{ t('admin.unionBlEmail') }}</th><th>{{ t('admin.unionBlSource') }}</th>
          <th>{{ t('admin.unionBlReason') }}</th><th>{{ t('admin.unionBlCreated') }}</th>
          <th>{{ t('admin.unionBlUntil') }}</th><th></th>
        </tr>
        <tr v-for="row in blRows" :key="row.id">
          <td>{{ row.id }}</td>
          <td>{{ row.email || '—' }}</td>
          <td>{{ row.source || '—' }}</td>
          <td>{{ row.reason || '—' }}</td>
          <td class="muted">{{ row.created_at || '—' }}</td>
          <td class="muted">{{ row.valid_until || '—' }}</td>
          <td>
            <button class="secondary" @click="blRelease(row)">{{ t('admin.unionBlRelease') }}</button>
            <button class="secondary" @click="blDelete(row)" style="margin-left: 8px; border-color: var(--danger); color: var(--danger);">
              {{ t('admin.unionBlDelete') }}
            </button>
          </td>
        </tr>
      </table>
      <div v-else class="muted">{{ t('admin.unionBlEmpty') }}</div>
      <!-- 上游 MUA API 为 Laravel 分页（默认 20 条/页），对齐老站翻页能力 -->
      <div class="row pager" v-if="blTotalPages > 1" style="margin-top: 12px; justify-content: flex-end;">
        <button class="secondary" :disabled="blPage <= 1" @click="blPage--; loadBlacklist()">{{ t('admin.prevPage') }}</button>
        <span class="muted" style="font-size: var(--fs-xs);">{{ blPage }} / {{ blTotalPages }}</span>
        <button class="secondary" :disabled="blPage >= blTotalPages" @click="blPage++; loadBlacklist()">{{ t('admin.nextPage') }}</button>
      </div>
      <div class="row" style="margin-top: 12px;">
        <input v-model="blEmail" :placeholder="t('admin.unionBlEmail')" style="max-width: 220px;" />
        <input v-model="blReason" :placeholder="t('admin.unionBlReason')" style="max-width: 260px;" />
        <button class="secondary" @click="blAdd">{{ t('admin.unionBlAdd') }}</button>
      </div>
    </div>

    <!-- 自助诊断 modal (1:1 with legacy yggdrasil-connect diagnoseModal) -->
    <div v-if="diagOpen" class="diag-overlay" @click.self="closeDiagnose">
      <div class="diag-modal">
        <div class="diag-head">
          <h3>{{ t('admin.unionOpDiagnose') }}</h3>
          <button class="diag-close" @click="closeDiagnose" aria-label="close">×</button>
        </div>
        <div class="diag-body">
          <div class="diag-idle" v-if="!diag && !diagLoading">
            <button class="diag-start" @click="triggerDiagnose">{{ t('admin.diagStart') }}</button>
          </div>
          <div class="diag-loading" v-if="diagLoading">
            <span class="diag-spinner"></span>
            <span>{{ t('admin.diagRunning') }}</span>
          </div>
          <div class="diag-result" v-if="diag && !diagLoading">
            <div class="diag-result-bar">
              <span class="diag-badge" :class="diag.status === 'ok' ? 'ok' : 'fail'">
                {{ diag.status === 'ok' ? t('admin.diagAllOk') : t('admin.diagHasFail') }}
              </span>
              <button class="diag-export" @click="exportDiagnose">⤓ {{ t('admin.diagExport') }}</button>
            </div>
            <table class="diag-table">
              <tr v-for="(r, i) in diagRows" :key="i">
                <th>{{ r.k }}</th>
                <td>
                  <span v-if="r.v === 'ok'" class="diag-badge ok">{{ t('admin.diagOk') }}</span>
                  <span v-else-if="r.v === 'fail'" class="diag-badge fail">{{ t('admin.diagFail') }}</span>
                  <textarea v-else-if="r.area" readonly rows="8" :value="r.area"></textarea>
                  <span v-else class="diag-mono">{{ r.v }}</span>
                </td>
              </tr>
            </table>
            <p class="diag-meta" v-if="diag.key_fingerprint">
              key_fingerprint: <span class="diag-mono">{{ diag.key_fingerprint }}</span>
              · private_key_version: <span class="diag-mono">{{ diag.private_key_version || '—' }}</span>
              · server_list_version: <span class="diag-mono">{{ diag.server_list_version || '—' }}</span>
            </p>
          </div>
        </div>
      </div>
    </div>
  </div>
</template>

<style scoped>
/* Blacklist table: fixed layout so the long free-text reason column cannot
   push created_at/actions off the card edge (2026-10-24 UI fix). */
.bl-table {
  table-layout: fixed;
  width: 100%;
}
.bl-table th:nth-child(1), .bl-table td:nth-child(1) { width: 44px; }
.bl-table th:nth-child(2), .bl-table td:nth-child(2) { width: 170px; }
.bl-table th:nth-child(3), .bl-table td:nth-child(3) { width: 64px; }
.bl-table th:nth-child(5), .bl-table td:nth-child(5) { width: 150px; }
.bl-table th:nth-child(6), .bl-table td:nth-child(6) { width: 130px; }
.bl-table td {
  word-break: break-all;
  vertical-align: top;
}

/* ---- 自助诊断 modal (1:1 legacy diagnoseModal, dark theme) ---- */
.diag-overlay {
  --scrim: rgba(0, 0, 0, 0.55);
  position: fixed;
  inset: 0;
  background: var(--scrim);
  display: flex;
  align-items: center;
  justify-content: center;
  z-index: 80;
}
.diag-modal {
  width: min(860px, 92vw);
  max-height: 86vh;
  overflow: auto;
  background: var(--panel);
  border: 1px solid var(--border);
  border-radius: 12px;
}
.diag-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 20px;
  border-bottom: 1px solid var(--border);
}
.diag-head h3 { margin: 0; font-size: var(--fs-m); }
.diag-close {
  background: none;
  border: none;
  color: var(--muted);
  font-size: var(--fs-l);
  cursor: pointer;
  line-height: 1.5;
}
.diag-body { padding: 20px; }
.diag-idle { text-align: center; padding: 24px 0; }
.diag-start {
  padding: 8px 24px;
  border-radius: var(--r-s);
  border: 1px solid var(--accent);
  background: var(--accent);
  color: var(--on-cinnabar);
  font-size: var(--fs-s);
  cursor: pointer;
}
.diag-loading {
  display: flex;
  gap: 12px;
  align-items: center;
  justify-content: center;
  padding: 32px 0;
}
.diag-spinner {
  width: 20px;
  height: 20px;
  border-radius: 50%;
  border: 3px solid var(--border);
  border-top-color: var(--accent);
  animation: diag-spin 0.9s linear infinite;
}
@keyframes diag-spin { to { transform: rotate(360deg); } }
.diag-result-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 12px;
}
.diag-badge {
  --ok-wash: color-mix(in srgb, var(--ok) 16%, transparent);
  --bad-wash: color-mix(in srgb, var(--danger) 14%, transparent);
  display: inline-block;
  padding: 4px 12px;
  border-radius: 24px;
  font-size: var(--fs-xs);
  font-weight: 600;
}
.diag-badge.ok { background: var(--ok-wash); color: var(--ok); border: 1px solid var(--ok); }
.diag-badge.fail { background: var(--bad-wash); color: var(--danger); border: 1px solid var(--danger); }
.diag-export {
  padding: 8px 12px;
  border-radius: var(--r-s);
  border: 1px solid var(--border);
  background: none;
  color: var(--text);
  cursor: pointer;
}
.diag-table { width: 100%; border-collapse: collapse; }
.diag-table th {
  width: 20%;
  text-align: right;
  padding: 8px 12px;
  color: var(--muted);
  font-weight: 500;
  vertical-align: top;
  border-bottom: 1px solid var(--border);
}
.diag-table td {
  padding: 8px 12px;
  vertical-align: top;
  border-bottom: 1px solid var(--border);
}
.diag-table textarea {
  width: 100%;
  min-height: 120px;
  resize: vertical;
  font-family: ui-monospace, Consolas, monospace;
  font-size: var(--fs-xs);
}
.diag-mono { font-family: ui-monospace, Consolas, monospace; font-size: var(--fs-xs); word-break: break-all; }
.diag-meta {
  margin: 12px 0 0;
  color: var(--muted);
  font-size: var(--fs-xs);
}
</style>
