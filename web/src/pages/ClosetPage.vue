<script setup>
// ClosetPage (A 级批次): blessing 衣柜 parity —— 皮肤/披风分 tab、即输即搜、
// 每目操作（设为头像 / 改名 / 移出）。数据自取自管（挂载即拉）。
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../api.js'
import { guard401 } from '../session.js'
import { promptDialog } from '../dialog.js'
import EmptyState from '../components/EmptyState.vue'
import Pager from '../components/Pager.vue'
import { shortHash } from '../textureLabel.js'

const { t } = useI18n()

const closet = ref([])
const tab = ref('all') // all | skin | cape
const query = ref('')
const err = ref('')
const ok = ref('')
const page = ref(1)
const total = ref(0)
const PAGE_SIZE = 50
let searchTimer = null

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / PAGE_SIZE)))

async function load() {
  err.value = ''
  try {
    const d = await api.closet(page.value, PAGE_SIZE, query.value.trim(), tab.value === 'all' ? '' : tab.value)
    closet.value = d.closet || []
    total.value = d.total || 0
  } catch (e) { if (!guard401(e)) err.value = e.message }
}

// Filtering and paging are the server's job now: uploads auto-collect, so a
// busy account's closet is as long as its texture list and a client-side
// filter over one page would silently hide matches.
function setTab(v) { tab.value = v; page.value = 1; load() }
function onQuery() {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => { page.value = 1; load() }, 300)
}
function goPage(p) {
  if (p < 1 || p > totalPages.value || p === page.value) return
  page.value = p
  load()
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
  const next = await promptDialog(t('closet.renamePrompt'), c.itemName || '')
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
    await load() // the page shifted; refetch rather than splice the row out
  } catch (e) { if (!guard401(e)) err.value = e.message }
}

onMounted(load)
</script>

<template>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('closet.title', { count: total }) }}</h2>
    <div class="row">
      <div class="tabs">
        <button class="tab" :class="{ active: tab === 'all' }" @click="setTab('all')">{{ t('closet.tabAll') }}</button>
        <button class="tab" :class="{ active: tab === 'skin' }" @click="setTab('skin')">{{ t('dashboard.typeSkin') }}</button>
        <button class="tab" :class="{ active: tab === 'cape' }" @click="setTab('cape')">{{ t('dashboard.typeCape') }}</button>
      </div>      <input :value="query" @input="query = $event.target.value; onQuery()" :placeholder="t('closet.searchPlaceholder')" style="max-width: 240px;" />
    </div>
    <div class="error" v-if="err">{{ err }}</div>
    <div class="success" v-if="ok">{{ ok }}</div>
    <div class="table-scroll" v-if="closet.length">
    <table style="margin-top: 10px;">
      <tr>
        <th>{{ t('dashboard.colPreview') }}</th>
        <th>{{ t('dashboard.colType') }}</th>
        <th>{{ t('closet.colName') }}</th>
        <th>{{ t('dashboard.materialName') }}</th>
        <th>{{ t('closet.colHash') }}</th>
        <th></th>
      </tr>
      <tr v-for="c in closet" :key="c.id">
        <td><img v-if="c.previewURL || c.url" :src="c.previewURL || c.url" class="thumb" alt="" loading="lazy" decoding="async" /><span v-else class="muted">—</span></td>
        <td>{{ c.type || '—' }}</td>
        <td>{{ c.itemName || '—' }}</td>
        <!-- Two different names, deliberately shown side by side: the alias is
             this user's own label, the texture name is shared by everyone. An
             empty alias used to leave the row with no readable name at all. -->
        <td class="tx-name">{{ c.description || shortHash(c.hash) }}</td>
        <td class="muted"><code class="hash">{{ shortHash(c.hash) }}</code></td>
        <td class="nowrap">
          <!-- The avatar endpoint rejects capes (422); offering the button on a
               cape row was a dead click. -->
          <button class="secondary" v-if="c.type === 'skin'" @click="setAsAvatar(c)">{{ t('closet.setAvatar') }}</button>
          <button class="secondary" style="margin-left: 6px;" @click="renameEntry(c)">{{ t('closet.rename') }}</button>
          <button class="secondary danger-outline" style="margin-left: 6px;" @click="removeEntry(c)">{{ t('closet.remove') }}</button>
        </td>
      </tr>
    </table>
    <Pager :page="page" :pages="totalPages" :total="total" @update:page="goPage" />
    </div>
    <EmptyState v-else :text="query || tab !== 'all' ? t('closet.emptyFiltered') : t('closet.empty')" />
  </div>
</template>

<style scoped>
.thumb { height: 48px; image-rendering: pixelated; vertical-align: middle; border-radius: 4px; }
.hash { font-size: var(--fs-xs); }
.tx-name { max-width: 220px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.nowrap { white-space: nowrap; }
</style>
