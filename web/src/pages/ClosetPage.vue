<script setup>
// ClosetPage (A 级批次): blessing 衣柜 parity —— 皮肤/披风分 tab、即输即搜、
// 每目操作（设为头像 / 改名 / 移出）。数据自取自管（挂载即拉）。
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../api.js'
import { guard401 } from '../session.js'

const { t } = useI18n()

const closet = ref([])
const tab = ref('all') // all | skin | cape
const query = ref('')
const err = ref('')
const ok = ref('')

const filtered = computed(() => closet.value.filter((c) => {
  if (tab.value !== 'all' && c.type !== tab.value) return false
  if (!query.value.trim()) return true
  const q = query.value.trim().toLowerCase()
  return (c.itemName || '').toLowerCase().includes(q) || (c.hash || '').toLowerCase().includes(q)
}))

async function load() {
  err.value = ''
  try {
    const d = await api.closet()
    closet.value = d.closet || []
  } catch (e) { if (!guard401(e)) err.value = e.message }
}

// 设为头像: the closet texture must still exist (hash present); orphan
// legacy rows (missing texture) cannot become an avatar — server would
// reject the ownership check anyway.
async function setAsAvatar(c) {
  err.value = ''; ok.value = ''
  if (!c.textureId) { err.value = t('closet.orphanNoAvatar'); return }
  try {
    await api.setAvatar(c.textureId)
    ok.value = t('closet.avatarSet')
  } catch (e) { if (!guard401(e)) err.value = e.message }
}

async function renameEntry(c) {
  const next = prompt(t('closet.renamePrompt'), c.itemName || '')
  if (next === null) return
  err.value = ''
  try {
    await api.closetRename(c.id, next)
    await load()
  } catch (e) { if (!guard401(e)) err.value = e.message }
}

async function removeEntry(c) {
  if (!confirm(t('closet.removeConfirm', { name: c.itemName || c.hash?.slice(0, 10) || '?' }))) return
  err.value = ''
  try {
    await api.closetRemove(c.id)
    closet.value = closet.value.filter((x) => x.id !== c.id)
  } catch (e) { if (!guard401(e)) err.value = e.message }
}

onMounted(load)
</script>

<template>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('closet.title', { count: closet.length }) }}</h2>
    <div class="row">
      <div class="tabs">
        <button class="tab" :class="{ active: tab === 'all' }" @click="tab = 'all'">{{ t('closet.tabAll') }}</button>
        <button class="tab" :class="{ active: tab === 'skin' }" @click="tab = 'skin'">{{ t('dashboard.typeSkin') }}</button>
        <button class="tab" :class="{ active: tab === 'cape' }" @click="tab = 'cape'">{{ t('dashboard.typeCape') }}</button>
      </div>
      <input v-model="query" :placeholder="t('closet.searchPlaceholder')" style="max-width: 240px;" />
    </div>
    <div class="error" v-if="err">{{ err }}</div>
    <div class="success" v-if="ok">{{ ok }}</div>
    <table v-if="filtered.length" style="margin-top: 10px;">
      <tr>
        <th>{{ t('dashboard.colPreview') }}</th>
        <th>{{ t('dashboard.colType') }}</th>
        <th>{{ t('closet.colName') }}</th>
        <th>{{ t('closet.colHash') }}</th>
        <th></th>
      </tr>
      <tr v-for="c in filtered" :key="c.id">
        <td><img v-if="c.url" :src="c.url" class="thumb" alt="" /><span v-else class="muted">—</span></td>
        <td>{{ c.type || '—' }}</td>
        <td>{{ c.itemName || '—' }}</td>
        <td class="muted"><code class="hash">{{ (c.hash || '—').slice(0, 16) }}</code></td>
        <td class="nowrap">
          <button class="secondary" @click="setAsAvatar(c)">{{ t('closet.setAvatar') }}</button>
          <button class="secondary" style="margin-left: 6px;" @click="renameEntry(c)">{{ t('closet.rename') }}</button>
          <button class="secondary danger-outline" style="margin-left: 6px;" @click="removeEntry(c)">{{ t('closet.remove') }}</button>
        </td>
      </tr>
    </table>
    <p class="muted" v-else>{{ query || tab !== 'all' ? t('closet.emptyFiltered') : t('closet.empty') }}</p>
  </div>
</template>

<style scoped>
.tabs { display: flex; gap: 6px; }
.tab {
  background: transparent; border: 1px solid var(--border); color: var(--muted);
  padding: 6px 14px; border-radius: 999px; cursor: pointer; font-size: 13px;
}
.tab.active { border-color: var(--accent); color: var(--text); font-weight: 600; }
.thumb { height: 48px; image-rendering: pixelated; vertical-align: middle; border-radius: 4px; }
.hash { font-size: 11px; }
.nowrap { white-space: nowrap; }
.danger-outline { border-color: var(--danger); color: var(--danger); }
.danger-outline:hover { background: var(--danger); color: var(--on-danger); }
</style>
