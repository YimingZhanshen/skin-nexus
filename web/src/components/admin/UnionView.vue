<script setup>
// UnionView: MUA Union module status + rotation resume + ops buttons (B5.3)
// + union blacklist proxy (B5.4). The module-disabled case shows an empty
// state. Release/Delete are irreversible-ish — both double-confirm, delete
// is permanent (hub erases the record).
import { ref, computed, onMounted, nextTick } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'
import { confirmDialog } from '../../dialog.js'
import { buildDiagModel, diagSummaryOf, diagMetaOf, diagJSONOf } from '../../diagModel.js'
import { formatWhen } from '../../format.js'
import Pager from '../Pager.vue'
import EmptyState from '../EmptyState.vue'
import UnionBlacklistSection from './UnionBlacklistSection.vue'

const { t } = useI18n()
const unionStatus = ref(null)
const error = ref('')
const loadFailed = ref(false)
const opResult = ref('')
const opBusy = ref(false)

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
  if (!(await confirmDialog(t('admin.unionResumeConfirm'), { danger: true }))) return
  try {
    await fetchJSON('/api/admin/v1/union/rotation/resume', { method: 'POST' })
    await load()
  } catch (e) { error.value = e.message }
}

async function runOp(name, path, confirmMsg) {
  if (confirmMsg && !(await confirmDialog(confirmMsg, { danger: true }))) return
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

// ---- 自助诊断 ----
// Three states (idle → running → result). The shell is the panel's own
// convention: a native <dialog> opened with showModal(). That is not a style
// preference, it is the fix for two measured problems with the hand-rolled
// fixed overlay this replaces:
//
//   1. An ancestor that animates transform captures position:fixed.
//      web/src/style.css applies `animation: view-rise … both` to
//      `.user-main > *`, which is exactly .admin-shell here; once the
//      animation has run, Chrome reports the computed transform as
//      matrix(1,0,0,1,0,0) rather than the keyword none, and a non-none
//      transform makes that ancestor the containing block. Measured on a
//      synthetic copy of this tree: the overlay then spans the panel's
//      1800px height instead of the 1145px viewport, so the modal is centred
//      ~400px below the fold and slides away when the operator scrolls.
//   2. The top layer is immune to all of that, and buys the focus trap, Esc
//      and the accessible name instead of re-implementing them badly.
const diagOpen = ref(false)
const diagLoading = ref(false)
const diag = ref(null) // {status, data, key_fingerprint, ...}
const diagDlg = ref(null)

function openDiagnose() {
  diag.value = null
  diagOpen.value = true
  nextTick(() => {
    if (diagDlg.value && !diagDlg.value.open) diagDlg.value.showModal()
  })
}

// One exit for every path — button, backdrop, Esc. The `close` event is NOT
// what resets the state: measured in a real Chrome against this very dialog,
// close() flips `open` to false and the queued close event never reaches the
// listener, so relying on it left a closed dialog node sitting in the DOM with
// diagOpen still true. Setting the flag here is the honest source of truth;
// @close stays as a safety net for any other way the element can close.
function closeDiagnose() {
  diagOpen.value = false
  diag.value = null
  const el = diagDlg.value
  if (el && el.open) el.close()
}

function onDiagClosed() {
  diagOpen.value = false
  diag.value = null
}

// Esc: `cancel` fires before the browser closes the dialog, so resetting the
// state here is enough — calling close() as well would only race the default.
function onDiagCancel() {
  diagOpen.value = false
  diag.value = null
}

// A click that lands on the dialog element itself came from the backdrop.
function onDiagClick(e) {
  if (e.target === diagDlg.value) closeDiagnose()
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

// The report model lives in ../../diagModel.js: it is pure, it is where the
// two-leg disambiguation happens, and it is asserted by scripts/diag-model.test.mjs
// (node --test, no new dependency). Kept out of this component on purpose — the
// flat-rows bug this replaces was invisible to every check that could see only
// markup.
const diagLegs = computed(() => (diag.value ? buildDiagModel(diag.value, t) : []))
const diagSummary = computed(() => diagSummaryOf(diagLegs.value))
const diagMeta = computed(() => diagMetaOf(diag.value, t))

// The report is opened on paper white, so the verdict colours are the dark
// theme's hues taken down to what measures AA on #fff (--ok is 2.23:1 there,
// --danger 3.90:1). They used to be Bootstrap's #198754 / #dc3545 — a passing
// contrast, but another product's palette inside ours.
const EXPORT_INK = { ok: '#2f7d4f', warn: '#8a6a2f', fail: '#a12b20', skip: '#6b6b6b' }

function htmlEscape(s) { return String(s).replace(/&/g, '&amp;').replace(/</g, '&lt;') }

function exportDiagnose() {
  // Same model as the screen, so a fix to what the operator sees cannot leave
  // the exported file describing the old, wrong thing. The state words come
  // from admin.diagState.*, the same four the deployment self-check uses.
  const stateWord = (s) => t('admin.diagState.' + s)
  const sections = diagLegs.value.map((leg) => {
    const rows = leg.rows.map((r) => {
      let cell = htmlEscape(r.v ?? '')
      if (r.mono) cell += ` <code>${htmlEscape(r.mono)}</code>`
      if (r.state) cell = `<span style="color:${EXPORT_INK[r.state]};font-weight:600;">${htmlEscape(stateWord(r.state))}</span> ${cell}`
      if (r.pre) cell = `<pre style="white-space:pre-wrap;word-break:break-all;background:#f4f4f5;padding:8px;">${htmlEscape(r.pre)}</pre>`
      return `<tr><th scope="row" style="text-align:left;vertical-align:top;width:22%;padding:8px 12px;">${htmlEscape(r.k)}</th><td style="padding:8px 12px;">${cell}</td></tr>`
    }).join('')
    return `<h3>${htmlEscape(leg.title)} — ${htmlEscape(stateWord(leg.state))}</h3><table style="border-collapse:collapse;width:100%;">${rows}</table>`
  }).join('')
  const meta = diagMeta.value.map((m) => `${htmlEscape(m.k)}: ${htmlEscape(m.v)}`).join(' · ')
  const title = htmlEscape(t('admin.diagExportTitle'))
  const html = `<!doctype html><html lang="${htmlEscape(document.documentElement.lang || 'zh-CN')}"><head><meta charset="utf-8"><title>${title}</title></head><body><h2>${title}</h2><p>${htmlEscape(formatWhen(new Date()))}</p><p>${meta}</p>${sections}</body></html>`
  downloadFile(`${t('admin.diagFileName')}-${diagStamp()}.html`, 'text/html;charset=utf-8', html)
}

// Both exports share one download path. Chromium tolerates clicking a detached
// anchor; the spec does not promise it, and revoking in the same tick can beat
// the write on a slow disk — so the anchor is attached and the URL outlives it.
function downloadFile(name, mime, content) {
  const url = URL.createObjectURL(new Blob([content], { type: mime }))
  const a = document.createElement('a')
  a.href = url
  a.download = name
  document.body.appendChild(a)
  a.click()
  a.remove()
  setTimeout(() => URL.revokeObjectURL(url), 60000)
}

function diagStamp() { return Math.round(Date.now() / 1000) }

// Machine-readable, for attaching to a hub bug report: the interpreted legs and
// the hub's untouched payload in one file.
function exportDiagnoseJSON() {
  const payload = diagJSONOf(diag.value, diagLegs.value, new Date().toISOString())
  downloadFile(`${t('admin.diagFileName')}-${diagStamp()}.json`, 'application/json;charset=utf-8', JSON.stringify(payload, null, 2))
}

onMounted(load)
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
      <div class="success" role="status" v-if="opResult">{{ opResult }}</div>

      <div class="muted" style="margin-top: 12px; font-size: var(--fs-xs);">
        {{ t('admin.unionTrustNotice') }}
      </div>
      <div class="error" role="alert" v-if="error">{{ error }}</div>
    </div>
    <div class="card" v-else-if="loadFailed">
      <div class="error" role="alert">{{ error }}</div>
      <button class="secondary" style="margin-top: 12px;" @click="load">{{ t('common.retry') }}</button>
    </div>
    <div class="card" v-else>
      <h2 style="margin-top: 0;">{{ t('admin.unionTitle') }}</h2>
      <div class="muted">{{ t('admin.unionDisabled') }}</div>
    </div>

    <UnionBlacklistSection :union-status="unionStatus" @error="error = $event" />

    <!-- 自助诊断: native <dialog> in the top layer (see openDiagnose) -->
    <dialog
      v-if="diagOpen"
      ref="diagDlg"
      class="diag-modal"
      :aria-label="t('admin.unionOpDiagnose')"
      @click="onDiagClick"
      @cancel="onDiagCancel"
      @close="onDiagClosed"
    >
      <div class="diag-head">
        <h3>{{ t('admin.unionOpDiagnose') }}</h3>
        <button class="ghost diag-close" @click="closeDiagnose" :aria-label="t('common.close')">×</button>
      </div>
      <div class="diag-body">
        <div class="diag-idle" v-if="!diag && !diagLoading">
          <button @click="triggerDiagnose">{{ t('admin.diagStart') }}</button>
        </div>
        <div class="diag-loading" v-if="diagLoading">
          <span class="spinner" aria-hidden="true"></span>
          <span>{{ t('admin.diagRunning') }}</span>
        </div>
        <div class="diag-result" v-if="diag && !diagLoading">
          <div class="diag-result-bar">
            <span class="diag-badge" :class="diagSummary">
              {{ diagSummary === 'ok' ? t('admin.diagAllOk') : diagSummary === 'warn' ? t('admin.diagHasWarn') : t('admin.diagHasFail') }}
            </span>
            <span class="diag-actions">
              <button class="ghost" :disabled="diagLoading" @click="triggerDiagnose">{{ t('admin.diagRecheck') }}</button>
              <button class="secondary" @click="exportDiagnoseJSON">{{ t('admin.diagExportJSON') }}</button>
              <button class="secondary" @click="exportDiagnose">⤓ {{ t('admin.diagExport') }}</button>
            </span>
          </div>
          <section class="diag-leg" v-for="leg in diagLegs" :key="leg.key">
            <h4 class="diag-leg-head">
              <span>{{ leg.title }}</span>
              <span class="diag-badge" :class="leg.state">{{ t('admin.diagState.' + leg.state) }}</span>
            </h4>
            <table class="diag-table">
              <caption class="sr-only">{{ leg.title }}</caption>
              <tr v-for="(r, i) in leg.rows" :key="i">
                <th scope="row">{{ r.k }}</th>
                <td>
                  <span v-if="r.state" class="diag-badge" :class="r.state">{{ t('admin.diagState.' + r.state) }}</span>
                  <span v-if="r.v"> {{ r.v }}</span>
                  <code v-if="r.mono" class="diag-mono">{{ r.mono }}</code>
                  <details v-if="r.pre" class="diag-raw">
                    <summary>{{ t('admin.diagRaw') }}</summary>
                    <pre class="diag-pre">{{ r.pre }}</pre>
                  </details>
                </td>
              </tr>
            </table>
          </section>
          <p class="diag-meta" v-if="diagMeta.length">
            <span v-for="m in diagMeta" :key="m.k" class="diag-meta-item">
              {{ m.k }}: <span class="diag-mono">{{ m.v }}</span>
            </span>
          </p>
        </div>
      </div>
    </dialog>
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

/* ---- 自助诊断: the native dialog itself is the panel, so there is no
   overlay element to position. Same scrim value as DialogPanel's ::backdrop.
   max-height is dvh, not vh: on a phone vh is the large viewport, and the
   bottom of the report would sit behind the browser bar. ---- */
.diag-modal {
  width: min(860px, calc(100% - 32px));
  max-height: 86dvh;
  overflow: auto;
  padding: 0;
  background: var(--panel);
  color: var(--text);
  border: 1px solid var(--border);
  border-radius: 12px;
}
.diag-modal::backdrop { background: rgba(0, 0, 0, 0.55); }
.diag-head {
  position: sticky;
  top: 0;
  z-index: 1;
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 16px 20px;
  border-bottom: 1px solid var(--border);
  /* Sticky over a scrolling report needs an opaque backing, or the first row
     shows through the title. */
  background: var(--panel);
}
.diag-head h3 { margin: 0; font-size: var(--fs-m); }
.diag-body { padding: 20px; }
.diag-idle { text-align: center; padding: 24px 0; }
.diag-loading {
  display: flex;
  gap: 12px;
  align-items: center;
  justify-content: center;
  padding: 32px 0;
}
.diag-result-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin-bottom: 12px;
}
.diag-actions { display: flex; gap: 8px; }
.diag-leg { margin-bottom: 20px; }
.diag-leg:last-of-type { margin-bottom: 0; }
.diag-leg-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  margin: 0 0 8px;
  font-size: var(--fs-s);
  font-weight: 500;
  color: var(--text);
}
.diag-badge {
  --ok-wash: color-mix(in srgb, var(--ok) 16%, transparent);
  --bad-wash: color-mix(in srgb, var(--danger) 14%, transparent);
  --warn-wash: color-mix(in srgb, var(--gold) 16%, transparent);
  display: inline-block;
  padding: 4px 12px;
  border-radius: 24px;
  font-size: var(--fs-xs);
  font-weight: 600;
}
.diag-badge.ok { background: var(--ok-wash); color: var(--ok); border: 1px solid var(--ok); }
.diag-badge.fail { background: var(--bad-wash); color: var(--danger); border: 1px solid var(--danger); }
/* 注意 is not a soft fail: an expiring certificate or a clock outside the
   signature window is actionable and neither means the hub is unreachable.
   The legacy modal only had success/danger, so every warning was a red pill. */
.diag-badge.warn { background: var(--warn-wash); color: var(--gold); border: 1px solid var(--gold); }
.diag-badge.skip { background: transparent; color: var(--muted); border: 1px solid var(--border); }
/* Fixed layout: the raw dumps are long single tokens, and with auto layout
   they push the label column off the card (same lesson as .bl-table above). */
.diag-table { width: 100%; border-collapse: collapse; table-layout: fixed; }
.diag-table th {
  width: 24%;
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
  word-break: break-word;
}
.diag-raw { margin-top: 8px; }
.diag-raw summary { cursor: pointer; color: var(--muted); font-size: var(--fs-xs); }
/* A read-only dump is not an input: the legacy textarea looked editable,
   could be typed into, and clipped to eight rows. */
.diag-pre {
  margin: 8px 0 0;
  padding: 12px;
  max-height: 260px;
  overflow: auto;
  background: var(--panel-2, var(--panel));
  border: 1px solid var(--border);
  border-radius: 8px;
  font-family: var(--mono, ui-monospace, Consolas, monospace);
  font-size: var(--fs-xs);
  white-space: pre-wrap;
  word-break: break-word;
}
.diag-mono { font-family: var(--mono, ui-monospace, Consolas, monospace); font-size: var(--fs-xs); word-break: break-all; }
.diag-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 16px;
  margin: 16px 0 0;
  padding-top: 12px;
  border-top: 1px solid var(--border);
  color: var(--muted);
  font-size: var(--fs-xs);
}
/* Phones: the label column would otherwise eat a quarter of a 375px screen
   and every value wraps to one word per line. Stack them instead. */
@media (max-width: 720px) {
  .diag-body { padding: 16px; }
  .diag-result-bar { flex-direction: column; align-items: flex-start; }
  .diag-table th { width: auto; text-align: left; padding-bottom: 0; border-bottom: none; }
  .diag-table td { padding-top: 4px; }
}
</style>
