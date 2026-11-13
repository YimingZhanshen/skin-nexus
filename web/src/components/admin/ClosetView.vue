<script setup>
// ClosetView: the operator's window into ONE player's closet — list, alias,
// remove. A player can already do all three for themselves; this exists for
// what a player cannot fix (orphan legacy rows, a stuck alias) and for
// checking what a report is about without asking the account to log in.
// The uid lives in the path, never in a query the handler would trust.
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { fetchJSON } from '../../adminApi.js'
import { confirmDialog, promptDialog } from '../../dialog.js'
import Pager from '../Pager.vue'
import EmptyState from '../EmptyState.vue'

const { t } = useI18n()

const uid = ref('')
const q = ref('')
const typ = ref('')
const rows = ref([])
const total = ref(0)
const page = ref(1)
const perPage = 20
const error = ref('')
const loaded = ref(false) // a list has been requested at least once
let seq = 0

const pages = computed(() => Math.max(1, Math.ceil(total.value / perPage)))

async function load() {
  const id = Number(uid.value)
  if (!Number.isInteger(id) || id <= 0) {
    rows.value = []
    total.value = 0
    loaded.value = true
    return
  }
  const my = ++seq
  error.value = ''
  try {
    const qs = new URLSearchParams({ page: String(page.value), perPage: String(perPage) })
    if (q.value.trim()) qs.set('q', q.value.trim())
    if (typ.value) qs.set('typ', typ.value)
    const d = await fetchJSON(`/api/admin/v1/users/${id}/closet?${qs}`)
    if (my !== seq) return
    rows.value = d.closet || []
    total.value = d.total || 0
    loaded.value = true
  } catch (e) {
    if (my === seq) error.value = e.message
  }
}

function search() {
  page.value = 1
  load()
}

async function rename(row) {
  const id = Number(uid.value)
  const current = row.itemName || row.description || ('#' + row.textureId)
  const next = await promptDialog(t('admin.closetRenamePrompt', { name: current }), row.itemName || '')
  if (next === null) return
  error.value = ''
  try {
    await fetchJSON(`/api/admin/v1/users/${id}/closet/${row.id}`, {
      method: 'PUT', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ itemName: next }),
    })
    await load()
  } catch (e) { error.value = e.message }
}

async function remove(row) {
  const id = Number(uid.value)
  if (!(await confirmDialog(t('admin.closetRemoveConfirm'), { danger: true }))) return
  error.value = ''
  try {
    await fetchJSON(`/api/admin/v1/users/${id}/closet/${row.id}`, { method: 'DELETE' })
    await load()
  } catch (e) { error.value = e.message }
}

function label(row) {
  // Orphan legacy rows have no texture metadata; the id is all that is left
  // and the entry still has to be identifiable (data-compat rule 7).
  return row.description || ('#' + row.textureId)
}

onMounted(() => { if (uid.value) load() })
</script>

<template>
  <div>
    <div class="row" style="justify-content: space-between; align-items: center;">
      <h2 style="margin: 0;">{{ t('admin.closetManage') }}</h2>
      <div class="row">
        <input v-model="uid" :placeholder="t('admin.closetUidPh')" :aria-label="t('admin.closetUidPh')"
               inputmode="numeric" style="max-width: 140px;" @keyup.enter="search" />
        <input v-model="q" :placeholder="t('admin.closetSearchPh')" :aria-label="t('admin.closetSearchPh')"
               style="max-width: 240px;" @input="search" />
        <select v-model="typ" :aria-label="t('admin.closetType')" @change="search" style="max-width: 130px;">
          <option value="">{{ t('dashboard.texTypeAll') }}</option>
          <option value="skin">{{ t('dashboard.texTypeSkin') }}</option>
          <option value="cape">{{ t('dashboard.texTypeCape') }}</option>
        </select>
        <button class="secondary" @click="search">{{ t('admin.reload') }}</button>
      </div>
    </div>
    <div class="error" role="alert" v-if="error">{{ error }}</div>

    <p class="muted" v-if="!loaded" style="margin-top: 16px;">{{ t('admin.closetNeedUid') }}</p>
    <table v-else-if="rows.length" style="margin-top: 12px;">
      <caption class="sr-only">{{ t('admin.closetManage') }}</caption>
      <tr>
        <th scope="col">{{ t('dashboard.colPreview') }}</th>
        <th scope="col">{{ t('admin.closetItem') }}</th>
        <th scope="col">{{ t('admin.closetAlias') }}</th>
        <th scope="col">{{ t('admin.closetType') }}</th>
        <th scope="col">{{ t('admin.closetHash') }}</th>
        <th scope="col"></th>
      </tr>
      <tr v-for="row in rows" :key="row.id">
        <td>
          <img v-if="row.previewURL" :src="row.previewURL" :alt="t('dashboard.colPreview')"
               style="height: 40px; image-rendering: pixelated;" loading="lazy" decoding="async" />
          <span v-else class="muted">—</span>
        </td>
        <td>{{ label(row) }}</td>
        <td>{{ row.itemName || '—' }}</td>
        <td>{{ row.type || '—' }}</td>
        <td class="muted" style="font-family: var(--mono);">{{ row.hash ? row.hash.slice(0, 12) : '—' }}</td>
        <td>
          <button class="secondary" @click="rename(row)">{{ t('admin.rename') }}</button>
          <button class="danger-outline" style="margin-left: 6px;" @click="remove(row)">{{ t('admin.delete') }}</button>
        </td>
      </tr>
    </table>
    <EmptyState v-else-if="loaded" :text="t('admin.closetEmpty')" style="margin-top: 12px;" />
    <Pager v-if="loaded && rows.length" :page="page" :pages="pages" :total="total"
           @update:page="(p) => { page = p; load() }" />
  </div>
</template>
