<script setup>
// UnionBlacklistSection.vue — the hub's blacklist, proxied through this
// station: search, page, release (invalidate) and delete.
//
// Release and delete are irreversible-ish — both double-confirm, delete is
// permanent (the hub erases the record). The section waits for the parent's
// status load before fetching: the card only exists when the module is on,
// and asking a disabled module for its blacklist is a 404 the operator
// should never see.
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'
import { confirmDialog } from '../../dialog.js'
import Pager from '../Pager.vue'
import EmptyState from '../EmptyState.vue'

const { t } = useI18n()
const props = defineProps({
  unionStatus: { type: Object, default: null },
})
// Errors surface in the parent's banner — the same shared spot they showed
// in before the split, so a failed blacklist call lands where the operator
// already looks. Empty string clears it, matching the old shared ref.
const emit = defineEmits(['error'])

// blacklist
const blRows = ref([])
const blTotal = ref(0)
const blPage = ref(1)
// hub 是 Laravel 分页（per_page 由 hub 定，不固定）——用上游权威的
// last_page，缺失时退化为 total/per_page 推算。
const blTotalPages = ref(1)
function blGoPage(p) {
  if (p < 1 || p > blTotalPages.value || p === blPage.value) return
  blPage.value = p
  loadBlacklist()
}
const blQ = ref('')
const blEmail = ref('')
const blReason = ref('')

async function loadBlacklist() {
  emit('error', '')
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
    emit('error', e.message)
  }
}

async function blAdd() {
  if (!blEmail.value.trim()) return
  emit('error', '')
  try {
    await fetchJSON('/api/admin/v1/union/blacklist', {
      method: 'POST',
      body: JSON.stringify({ email: blEmail.value.trim(), reason: blReason.value.trim() }),
    })
    blEmail.value = ''
    blReason.value = ''
    await loadBlacklist()
  } catch (e) { emit('error', e.message) }
}

async function blRelease(row) {
  if (!(await confirmDialog(t('admin.unionBlReleaseConfirm', { email: row.email || row.id }), { danger: true }))) return
  emit('error', '')
  try {
    await fetchJSON(`/api/admin/v1/union/blacklist/${encodeURIComponent(row.id)}/invalidate`, { method: 'PUT' })
    await loadBlacklist()
  } catch (e) { emit('error', e.message) }
}

async function blDelete(row) {
  if (!(await confirmDialog(t('admin.unionBlDeleteConfirm1', { email: row.email || row.id }), { danger: true }))) return
  if (!(await confirmDialog(t('admin.unionBlDeleteConfirm2'), { danger: true }))) return
  emit('error', '')
  try {
    await fetchJSON(`/api/admin/v1/union/blacklist/${encodeURIComponent(row.id)}`, { method: 'DELETE' })
    await loadBlacklist()
  } catch (e) { emit('error', e.message) }
}

watch(() => props.unionStatus, (v) => { if (v) loadBlacklist() }, { immediate: true })
</script>

<template>
    <!-- B5.4 blacklist -->
    <div class="card" v-if="unionStatus">
      <h2 style="margin-top: 0;">{{ t('admin.unionBlTitle') }}</h2>
      <div class="row">
        <input v-model="blQ" :placeholder="t('admin.unionBlSearch')" style="max-width: 220px;" @keyup.enter="blPage = 1; loadBlacklist()" />
        <button class="secondary" @click="blPage = 1; loadBlacklist()">{{ t('admin.unionBlSearchBtn') }}</button>
      </div>
      <div class="table-scroll" v-if="blRows.length" style="margin-top: 12px;">
      <table class="bl-table">
        <caption class="sr-only">{{ t('admin.unionBlTitle') }}</caption>
        <tr>
          <th scope="col">ID</th><th scope="col">{{ t('admin.unionBlEmail') }}</th><th scope="col">{{ t('admin.unionBlSource') }}</th>
          <th scope="col">{{ t('admin.unionBlReason') }}</th><th scope="col">{{ t('admin.unionBlCreated') }}</th>
          <th scope="col">{{ t('admin.unionBlUntil') }}</th><th scope="col"></th>
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
            <button class="secondary danger-outline" style="margin-left: 8px;" @click="blDelete(row)">
              {{ t('admin.unionBlDelete') }}
            </button>
          </td>
        </tr>
      </table>
      </div>
      <EmptyState v-else :text="t('admin.unionBlEmpty')" />
      <!-- 上游 MUA API 为 Laravel 分页（默认 20 条/页），对齐老站翻页能力 -->
      <Pager :page="blPage" :pages="blTotalPages" align="end" @update:page="blGoPage" />
      <div class="row" style="margin-top: 12px;">
        <input v-model="blEmail" :placeholder="t('admin.unionBlEmail')" style="max-width: 220px;" />
        <input v-model="blReason" :placeholder="t('admin.unionBlReason')" style="max-width: 260px;" />
        <button class="secondary" @click="blAdd">{{ t('admin.unionBlAdd') }}</button>
      </div>
    </div>
</template>
