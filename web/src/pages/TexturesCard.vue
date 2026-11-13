<script setup>
// TexturesCard.vue — the player's own texture list: search, paging, and the
// three row actions (wear, delete, correct the arm model).
//
// It fetches its own rows. The page keeps the account half of the old
// combined load, and asks this card to refetch after an upload (reload also
// highlights the new row). Errors are emitted, not rendered here: the page's
// banner above the cards is where this surface has always shown them, so a
// failed 入柜 lands in the same place it did before.
import { ref, computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../api.js'
import { confirmDialog } from '../dialog.js'
import { guard401 } from '../session.js'
import EmptyState from '../components/EmptyState.vue'
import Pager from '../components/Pager.vue'

const { t } = useI18n()
const props = defineProps({
  pageErr: { type: String, default: '' },
})
const emit = defineEmits(['error', 'choose-file'])

// The page's banner above the cards is where this surface has always shown
// its errors; the EmptyState below reads the same text so a failed load never
// offers "choose a file" as if nothing were wrong.
const err = ref('')
const pageErr = computed(() => props.pageErr || err.value)
function fail(message) {
  err.value = message
  emit('error', message)
}

const textures = ref([])
// The texture list is paginated server-side: an account with 700 uploads used to
// render every row at once. `inCloset` rides along per row, so the 入柜 state no
// longer needs the whole closet in memory.
const TEX_PAGE_SIZE = 50
const texPage = ref(1)
const texTotal = ref(0)
// 与衣柜同一套：即输即搜 + 类型筛选。名字与内容地址都能搜——导入批次给的
// 是「xxx · 来源 littleskin.cn#123」这种长名字，玩家记得住的往往只有片段。
const texQ = ref('')
const texType = ref('')
const newTexId = ref(0)
// Rows with a write in flight. The three row handlers below each read the row's
// own state to decide what to send, so a second click before the answer lands
// sends the same patch again — and for a delete, it opens a second confirmation
// for a texture that is already gone.
const rowBusy = ref(new Set())
const setRowBusy = (id, on) => {
  const next = new Set(rowBusy.value)
  if (on) next.add(id)
  else next.delete(id)
  rowBusy.value = next
}
let texTimer = 0
let texSeq = 0

function searchTextures() {
  clearTimeout(texTimer)
  texTimer = setTimeout(() => { texPage.value = 1; newTexId.value = 0; load() }, 300)
}

function clearTexSearch() {
  texQ.value = ''
  texType.value = ''
  texPage.value = 1
  load()
}

const texPages = computed(() => Math.max(1, Math.ceil(texTotal.value / TEX_PAGE_SIZE)))
function goTexPage(p) {
  if (p < 1 || p > texPages.value || p === texPage.value) return
  texPage.value = p
  newTexId.value = 0
  load()
}

async function addToCloset(tx) {
  if (rowBusy.value.has(tx.id)) return
  err.value = ''
  setRowBusy(tx.id, true)
  try {
    await api.closetAdd(tx.id)
    tx.inCloset = true // the list row carries its own state; no full closet fetch
  } catch (e) { if (!guard401(e)) fail(e.message) } // 入柜失败显示在纹理卡区（页级横幅）
  finally { setRowBusy(tx.id, false) }
}

// 删除自己的材质：先不带 force，409 会带回「谁还在用它」，用户确认后再解绑删除。
async function removeTexture(tx) {
  if (rowBusy.value.has(tx.id)) return
  if (!(await confirmDialog(t('dashboard.deleteConfirm', { name: tx.description || ('#' + tx.id) }), { danger: true }))) return
  err.value = ''
  setRowBusy(tx.id, true)
  try {
    try {
      await api.textureDelete(tx.id)
    } catch (e) {
      if (e.status !== 409 || !e.data || !Array.isArray(e.data.players)) throw e
      const detail = t('dashboard.deleteBlocked', {
        players: (e.data.players || []).join('、') || '—',
        closets: e.data.closets || 0,
        avatars: e.data.avatars || 0,
      })
      // The blocker list is the heading, the consequence is the question: the
      // native version stuffed both into one window.confirm string with a \n
      // that no longer survives a real dialog.
      if (!(await confirmDialog(t('dashboard.deleteForceConfirm'), { title: detail, danger: true }))) return
      await api.textureDelete(tx.id, true)
    }
    await load()
  } catch (e) { if (!guard401(e)) fail(e.message) }
  finally { setRowBusy(tx.id, false) }
}

// 纠正手臂模型：只有旧式 64×32 材质判不出来，其余情况服务端已经替用户判对。
async function setModel(tx, model) {
  if (rowBusy.value.has(tx.id) || tx.model === model) return
  err.value = ''
  setRowBusy(tx.id, true)
  try {
    await api.skinlibPatch(tx.id, { model })
    tx.model = model
  } catch (e) { if (!guard401(e)) fail(e.message) }
  finally { setRowBusy(tx.id, false) }
}
async function load() {
  const mine = ++texSeq
  try {
    const tx = await api.textures(texPage.value, TEX_PAGE_SIZE, texQ.value.trim(), texType.value)
    if (mine !== texSeq) return // 搜索连打时旧响应不能覆盖新结果
    textures.value = tx.textures || []
    texTotal.value = tx.total || 0
    // 删掉末页最后一条后停在一个空页，以前只剩「还没有上传过材质」——玩家会以为
    // 全部材质都没了。页码超出总数就退回最后一页再取一次。
    const pages = Math.max(1, Math.ceil((tx.total || 0) / TEX_PAGE_SIZE))
    if (!(tx.textures || []).length && tx.total > 0 && texPage.value > pages) {
      texPage.value = pages
      return load()
    }
  } catch (e) { if (mine === texSeq && !guard401(e)) fail(e.message) } // 数据加载失败≠上传失败
}

onMounted(() => reload())

// After an upload: refetch and mark the row that just arrived.
function reload(newId) {
  newTexId.value = newId || 0
  texPage.value = 1
  return load()
}
defineExpose({ reload })
</script>

<template>
  <!-- textures -->
  <div class="card">
    <h2>{{ t('dashboard.texturesTitle', { count: texTotal }) }}</h2>
    <div class="row" style="gap: 8px; flex-wrap: wrap;">
      <input :value="texQ" :placeholder="t('dashboard.texSearchPlaceholder')" :aria-label="t('dashboard.texSearchPlaceholder')"
             style="flex: 1 1 220px; max-width: 360px;" @input="texQ = $event.target.value; searchTextures()" />
      <select v-model="texType" :aria-label="t('dashboard.texTypeLabel')" @change="searchTextures()" style="max-width: 160px;">
        <option value="">{{ t('dashboard.texTypeAll') }}</option>
        <option value="skin">{{ t('dashboard.texTypeSkin') }}</option>
        <option value="cape">{{ t('dashboard.texTypeCape') }}</option>
      </select>
      <button class="secondary" v-if="texQ || texType" @click="clearTexSearch">{{ t('dashboard.texClear') }}</button>
    </div>
    <div class="table-scroll" v-if="textures.length">
    <table>
      <caption class="sr-only">{{ t('dashboard.texturesTitle', { count: texTotal }) }}</caption>
      <tr><th scope="col">{{ t('dashboard.materialName') }}</th><th scope="col">{{ t('dashboard.colType') }}</th><th scope="col">{{ t('dashboard.colModel') }}</th><th scope="col">{{ t('dashboard.colPreview') }}</th><th scope="col">{{ t('dashboard.colHash') }}</th><th scope="col"></th></tr>
      <tr v-for="tx in textures" :key="tx.id" :class="{ 'row-new': tx.id === newTexId }">
        <td class="tx-name" :title="tx.description || tx.hash">{{ textureLabel(tx) }}</td>
        <td>{{ tx.type }}</td>
        <td>
          <!-- 64×32 旧格式没有可判定的像素，识别只会给出 default；这里保留纠正入口 -->
          <select v-if="tx.type === 'skin'" class="model-fix" :value="tx.model" :disabled="rowBusy.has(tx.id)"
                  :aria-label="t('dashboard.colModel')" @change="setModel(tx, $event.target.value)">
            <option value="default">{{ t('dashboard.modelDefault') }}</option>
            <option value="slim">{{ t('dashboard.modelSlim') }}</option>
          </select>
          <span v-else>{{ tx.model || '—' }}</span>
        </td>
        <td><img :src="tx.previewURL || tx.url" :alt="t('dashboard.colPreview')" :title="tx.type === 'cape' ? t('common.capeFlat') : ''" style="height: 48px; image-rendering: pixelated;" loading="lazy" decoding="async" /></td>
        <td class="muted">{{ shortHash(tx.hash) }}</td>
        <td>
          <div class="row" style="gap: 6px; flex-wrap: wrap;">
            <button class="secondary" :disabled="rowBusy.has(tx.id)" v-if="!tx.inCloset" @click="addToCloset(tx)">{{ t('dashboard.closetAdd') }}</button>
            <span class="muted" v-else style="font-size: var(--fs-xs);">{{ t('dashboard.closetIn') }}</span>
            <button class="danger-outline" :disabled="rowBusy.has(tx.id)" @click="removeTexture(tx)">{{ t('dashboard.textureDelete') }}</button>
          </div>
        </td>
      </tr>
    </table>
    </div>
    <EmptyState v-else-if="!pageErr" :text="texQ || texType ? t('dashboard.texNoMatch') : t('dashboard.texturesEmpty')" :hint="t('dashboard.texturesHint')">
      <button @click="pickFile">{{ t('dashboard.chooseFile') }}</button>
    </EmptyState>
    <Pager :page="texPage" :pages="texPages" :total="texTotal" @update:page="goTexPage" />
  </div>
</template>
