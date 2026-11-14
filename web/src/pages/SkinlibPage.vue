<script setup>
// Skinlib — public texture library (blessing skinlib.index parity).
// Like heart = closet add/remove (legacy semantics). Owner sees rename /
// visibility toggle on their own entries.
import { computed, ref, onMounted, inject } from 'vue'
import { useI18n } from 'vue-i18n'
import { api } from '../api.js'
import { guard401 } from '../session.js'
import { confirmDialog, promptDialog } from '../dialog.js'
import { shortHash } from '../textureLabel.js'
import PluginSlot from '../components/plugin/PluginSlot.vue'
import Pager from '../components/Pager.vue'
import EmptyState from '../components/EmptyState.vue'
import WearTargetPicker from '../components/WearTargetPicker.vue'

const { t } = useI18n()
const emit = defineEmits(['login'])
// App.vue already resolved /me during boot; re-fetching here cost every
// visitor an extra RTT on high-latency links.
const props = defineProps({ user: { type: Object, default: null } })
const goto = inject('gotoView', null)

const items = ref([])
const total = ref(0)
const page = ref(1)
const perPage = 20
const filter = ref('skin')
const sort = ref('time')
const keyword = ref('')
const mine = ref(false)
const loading = ref(false)
const err = ref('')
const ok = ref('')
const justLiked = ref(null)  // the ♥ that just landed, so the page can offer the next step
// Rows with a write in flight. Every one of these handlers reads the row's
// current value to compute the next one, so a second click before the first
// answer lands sends the same patch twice and reports 「已保存」 for a change
// that did not happen.
const busyTid = ref(new Set())
const setBusy = (tid, on) => {
  const next = new Set(busyTid.value)
  if (on) next.add(tid)
  else next.delete(tid)
  busyTid.value = next
}
// The server-rendered detail page hands its tid back here as
// /skinlib#wear-12-skin: the wearer picker lives in this page, not in that one,
// and duplicating it in the Go template would be a second implementation of the
// same flow. Separate from justLiked because that row says「已收进衣柜」, and
// someone arriving over here never clicked the heart.
const wearTarget = ref(null)
const pickerItem = computed(() => justLiked.value || wearTarget.value)
const wearOpen = ref(false)
const me = computed(() => props.user)

const totalPages = computed(() => Math.max(1, Math.ceil(total.value / perPage)))
const canEdit = (it) => me.value && (me.value.id === it.uploader || me.value.permission > 0)
const sourceBadge = (source) => ({
  original: t('dashboard.sourceOriginalBadge'),
  derivative: t('dashboard.sourceDerivativeBadge'),
  repost: t('dashboard.sourceRepostBadge'),
}[source] || '')

let reqSeq = 0
async function load() {
  const seq = ++reqSeq
  loading.value = true
  err.value = ''
  try {
    const data = await api.skinlib({
      filter: filter.value,
      sort: sort.value,
      keyword: keyword.value.trim(),
      page: page.value,
      uploader: mine.value && me.value ? me.value.id : '',
    })
    if (seq !== reqSeq) return // a newer tab/sort/page request won
    items.value = data.items || []
    total.value = data.total || 0
  } catch (e) {
    if (seq !== reqSeq) return
    if (!guard401(e)) err.value = e.message
  } finally {
    if (seq === reqSeq) loading.value = false
  }
}

let searchTimer = null
function onSearch() {
  clearTimeout(searchTimer)
  searchTimer = setTimeout(() => { page.value = 1; load() }, 300)
}

function setFilter(f) { filter.value = f; page.value = 1; load() }
function goUpload() {
  location.href = '/user/dashboard#upload'
}

function setSort(s) { sort.value = s; page.value = 1; load() }
function toggleMine() { mine.value = !mine.value; page.value = 1; load() }
function goPage(p) {
  if (p < 1 || p > totalPages.value || p === page.value) return
  page.value = p
  load()
}

async function toggleLike(it) {
  // 访客点 ♥ = 明确登录引导（2026-10-27 逻辑修复：原只塞一行红字）
  if (!me.value) { emit('login'); return }
  if (busyTid.value.has(it.tid)) return
  err.value = ''
  if (it.liked) {
    // The heart is worded as「收进衣柜」, so un-clicking it takes something out of
    // the closet. One silent click that vanished a row the player didn't mean to
    // drop reads as a mis-click; ask once, in the same closet words.
    const name = it.name || ('#' + it.tid)
    if (!(await confirmDialog(t('skinlib.unlikeConfirm', { name })))) return
  }
  setBusy(it.tid, true)
  try {
    if (it.liked) {
      const res = await api.skinlibUnlike(it.tid)
      it.liked = false
      it.likes = res.likes
      justLiked.value = null
    } else {
      const res = await api.skinlibLike(it.tid)
      it.liked = true
      it.likes = res.likes
      // ♥ is how a player finds something they want to wear. Stopping at "the
      // heart turned red" left the next step — the one they wanted it for —
      // undiscoverable.
      justLiked.value = it
    }
  } catch (e) {
    if (!guard401(e)) err.value = e.message
  } finally {
    setBusy(it.tid, false)
  }
}

async function togglePublic(it) {
  if (busyTid.value.has(it.tid)) return
  err.value = ''
  ok.value = ''
  setBusy(it.tid, true)
  try {
    await api.skinlibPatch(it.tid, { public: !it.public })
    it.public = !it.public
    ok.value = t('common.saved')
  } catch (e) {
    if (!guard401(e)) err.value = e.message
  } finally {
    setBusy(it.tid, false)
  }
}

async function renameItem(it) {
  if (busyTid.value.has(it.tid)) return
  const next = await promptDialog(t('skinlib.renamePrompt'), it.name || '')
  if (next === null) return
  err.value = ''
  ok.value = ''
  setBusy(it.tid, true)
  try {
    await api.skinlibPatch(it.tid, { name: next.trim() })
    it.name = next.trim()
    ok.value = t('common.saved')
  } catch (e) {
    if (!guard401(e)) err.value = e.message
  } finally {
    setBusy(it.tid, false)
  }
}

// Server-rendered preview PNG (avatargen /preview/{tid}: back+front views).
// One <img> per card — a skinview3d canvas per card burns a WebGL context
// each and the grid exceeds the browser's ~16-context budget: oldest
// contexts get dropped (white cards + "Context Lost" + renderer crashes).
// The API hands back a versioned previewURL (?v=rendererVersion) so the
// year-long immutable cache re-fetches after a rasterizer fix.
// Capes have no body render; serve the flat texture instead.
function previewURL(it) {
  // The versioned preview URL comes from the server; a hand-written
  // `/preview/{tid}` here would ignore the renderer version and serve a
  // year-cached stale image after a rasterizer bump. The content-addressed
  // original is the honest fallback (its URL already is its bytes).
  return it.type === 'skin' ? (it.previewURL || it.textureURL) : it.textureURL
}

function wearFromHash() {
  const m = /^#wear-(\d+)-(skin|cape)$/.exec(location.hash || '')
  if (!m) return
  const tid = Number(m[1])
  const it = items.value.find((x) => x.tid === tid)
  wearTarget.value = { tid, type: m[2], name: it ? it.name : '' }
  wearOpen.value = true
  // Drop it: a reload of a URL that still says #wear-… would pop the dialog
  // again at a moment the player didn't ask for.
  history.replaceState(null, '', location.pathname)
}

onMounted(async () => {
  await load()
  wearFromHash()
})
</script>

<template>
  <nav class="crumb" :aria-label="t('common.breadcrumb')">
    <a href="/">{{ t('nav.home') }}</a><span class="sep" aria-hidden="true">›</span><span aria-current="page">{{ t('nav.skinlib') }}</span>
  </nav>
  <div class="card">
    <h2 style="margin-top: 0;">{{ t('skinlib.title') }}</h2>
    <p class="muted">{{ t('skinlib.subtitle') }}</p>

    <div class="row" style="flex-wrap: wrap; gap: 8px; align-items: center;">
      <div class="tabs">
        <button class="tab" :class="{ active: filter === 'skin' }" @click="setFilter('skin')">{{ t('skinlib.tabSkin') }}</button>
        <button class="tab" :class="{ active: filter === 'cape' }" @click="setFilter('cape')">{{ t('skinlib.tabCape') }}</button>
        <button class="tab" :class="{ active: filter === 'steve' }" @click="setFilter('steve')">{{ t('skinlib.tabSteve') }}</button>
        <button class="tab" :class="{ active: filter === 'alex' }" @click="setFilter('alex')">{{ t('skinlib.tabAlex') }}</button>
      </div>
      <input v-model="keyword" :placeholder="t('skinlib.searchPlaceholder')" style="max-width: 220px;" @input="onSearch" />
      <select :value="sort" style="max-width: 140px;" @change="setSort($event.target.value)">
        <option value="time">{{ t('skinlib.sortTime') }}</option>
        <option value="likes">{{ t('skinlib.sortLikes') }}</option>
      </select>
      <button v-if="me" class="secondary" :class="{ 'mine-on': mine }" @click="toggleMine">{{ t('skinlib.mineOnly') }}</button>
      <!-- 上传是玩家来这里最常做的事，却藏在仪表盘第 3 张卡下面（站长的原话）。
           工具条右侧给一个入口，深链到上传卡并聚焦选文件按钮。 -->
      <button v-if="me" @click="goUpload">{{ t('skinlib.uploadCta') }}</button>
    </div>
    <PluginSlot slot="skinlib.list.toolbar" />

    <div class="error" role="alert" v-if="err">{{ err }}</div>
    <div class="success" role="status" v-if="ok">{{ ok }}</div>
    <div class="row wear-next" v-if="justLiked">
      <span class="muted">{{ t('skinlib.closetNow', { name: justLiked.name || ('#' + justLiked.tid) }) }}</span>
      <button class="secondary" @click="wearOpen = true">{{ t('wear.button') }}</button>
      <button class="secondary" @click="justLiked = null">✕</button>
    </div>

    <div class="skinlib-grid" v-if="loading" aria-hidden="true">
      <div class="skin-card" v-for="n in 8" :key="n">
        <div class="skin-card-prev skeleton"></div>
        <div class="skin-card-body">
          <div class="skeleton skel-line"></div>
          <div class="skeleton skel-line short"></div>
        </div>
      </div>
    </div>
    <EmptyState v-else-if="!items.length" :text="t('skinlib.empty')" :hint="t('skinlib.emptyHint')">
      <button v-if="me" @click="goUpload">{{ t('skinlib.goUpload') }}</button>
    </EmptyState>

    <div class="skinlib-grid" v-else>
      <div class="skin-card" v-for="it in items" :key="it.tid">
        <div class="skin-card-prev">
          <a :href="'/skinlib/' + it.tid" :title="it.name || ('#' + it.tid)">
            <img v-if="it.type === 'skin'" :src="previewURL(it)" :alt="it.name" class="skin-preview-img" loading="lazy" decoding="async" />
            <img v-else :src="previewURL(it)" :alt="it.name" :title="t('common.capeFlat')" class="cape-thumb" loading="lazy" decoding="async" />
          </a>
        </div>
        <div class="skin-card-body">
          <div class="skin-card-name" :title="it.name">
            <span v-if="!it.public" class="lock" :title="t('skinlib.privateTip')">{{ t('skinlib.privateBadge') }}</span>
            <a :href="'/skinlib/' + it.tid" class="skin-card-link">{{ it.name || `#${it.tid}` }}</a>
          </div>
          <code class="hash-plate" :title="it.hash">{{ shortHash(it.hash) }}</code>
          <div class="skin-card-meta">
            <!-- 披风无模型之分，不渲染 Steve/Alex 徽章（cape 空 model 被误判 Steve） -->
            <span v-if="it.type !== 'cape'" class="badge" :class="it.model === 'slim' ? 'alex' : 'steve'">
              {{ it.model === 'slim' ? 'Alex' : 'Steve' }}
            </span>
            <span v-if="sourceBadge(it.source)" class="badge source-badge" :class="'source-' + it.source">
              {{ sourceBadge(it.source) }}
            </span>
            <span class="uploader">{{ it.nickname }}</span>
          </div>
          <div class="skin-card-actions">
            <button class="like-btn" :class="{ liked: it.liked }" :disabled="busyTid.has(it.tid)"
                    :title="t(it.liked ? 'skinlib.likedTip' : 'skinlib.likeTip')" @click="toggleLike(it)">
              <!-- 线性心形与导航图标同语言（G7）；文案里的 ♥ 指代本按钮，不涉 -->
              <svg class="like-ico" viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor"
                   stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 20.5C12 20.5 3.5 15 3.5 8.8 3.5 5.9 5.8 4 8.3 4c1.7 0 3 .9 3.7 2.1C12.7 4.9 14 4 15.7 4c2.5 0 4.8 1.9 4.8 4.8 0 6.2-8.5 11.7-8.5 11.7z"/></svg>
              {{ it.likes }}<span class="like-state" v-if="it.liked">{{ t('skinlib.closetBadge') }}</span>
            </button>
            <template v-if="canEdit(it)">
              <button class="ghost" :disabled="busyTid.has(it.tid)" @click="renameItem(it)">{{ t('skinlib.rename') }}</button>
              <button class="ghost" :disabled="busyTid.has(it.tid)" @click="togglePublic(it)">
                {{ busyTid.has(it.tid) ? t('skinlib.saving') : (it.public ? t('skinlib.makePrivate') : t('skinlib.makePublic')) }}
              </button>
            </template>
          </div>
        </div>
      </div>
    </div>

    <Pager :page="page" :pages="totalPages" :total="total" @update:page="goPage" />
  </div>

  <WearTargetPicker
    :open="wearOpen"
    :type="pickerItem ? pickerItem.type : 'skin'"
    :texture-id="pickerItem ? pickerItem.tid : 0"
    :texture-name="pickerItem ? (pickerItem.name || '') : ''"
    @close="wearOpen = false; wearTarget = null"
    @worn="justLiked = null; wearTarget = null; wearOpen = false"
    @need-player="wearOpen = false; justLiked = null; wearTarget = null; goto && goto('players')"
  />
</template>

<style scoped>
.wear-next { align-items: center; gap: 12px; margin-top: 12px; flex-wrap: wrap; }
.mine-on { border-color: var(--accent) !important; color: var(--accent) !important; }
.source-badge { background: var(--panel-2); color: var(--muted); border: 1px solid var(--border); }
.source-original { color: var(--jade, #7dd3a0); border-color: currentColor; }
.source-derivative { color: var(--gold, #d5a84f); border-color: currentColor; }
.source-repost { color: var(--accent); border-color: currentColor; }

.skinlib-grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(200px, 100%), 1fr));
  gap: 16px; margin-top: 16px;
  /* 材质徽章色（scoped 令牌，供子组件样式引用） */
  --steve-bg: rgba(47, 191, 107, .16); --steve-ink: #7ee2a8;
  --alex-bg: rgba(224, 166, 60, .16); --alex-ink: #ecc288;
}
.skin-card {
  border: 1px solid var(--border); border-radius: var(--r-m); overflow: hidden;
  background: var(--bg); display: flex; flex-direction: column;
  transition: border-color var(--t-fast) var(--ease), box-shadow var(--t-mid) var(--ease);
}
.skin-card:hover { border-color: var(--line-strong); box-shadow: var(--shadow-lift); }
/* the scoped .like-btn min-height outspecifies the global coarse-pointer rule */
.like-ico { vertical-align: -3px; margin-right: 4px; }
@media (pointer: coarse) {
  .like-btn { min-height: 44px; padding-inline: 16px; }
}
.skin-card-prev {
  height: 180px; display: flex; align-items: center; justify-content: center;
  background: var(--panel-2);
}
.skin-preview-img { height: 152px; image-rendering: pixelated; object-fit: contain; transition: transform var(--t-mid) var(--ease); }
.cape-thumb { max-width: 90%; max-height: 152px; image-rendering: pixelated; transition: transform var(--t-mid) var(--ease); }
.skin-card:hover .skin-preview-img, .skin-card:hover .cape-thumb { transform: scale(1.04); }
.skin-card-body { padding: 12px; display: flex; flex-direction: column; gap: 8px; }
.skin-card-name {
  font-weight: 600; font-size: var(--fs-s); white-space: nowrap;
  overflow: hidden; text-overflow: ellipsis;
}
.skin-card-link { color: inherit; text-decoration: none; }
.skin-card-link:hover { color: var(--accent); }
/* 签名铭牌：12 位短哈希，完整值在 title 里（与详情页 meta 同款） */
.hash-plate {
  font-family: var(--mono, ui-monospace, monospace);
  font-size: var(--fs-xs); color: var(--muted);
  letter-spacing: .04em; cursor: default;
  overflow: hidden; text-overflow: ellipsis; white-space: nowrap;
}
.skin-card-prev a { display: flex; align-items: center; justify-content: center; height: 100%; }
/* 私密标记：方块小徽章（原 emoji 锁按中文 UI 规范去图标化） */
.lock {
  display: inline-block; margin-right: 4px; padding: 0 4px;
  border: 1px solid var(--gold); border-radius: var(--r-s);
  font-size: var(--fs-xs); font-weight: 600; color: var(--gold); line-height: 1.5;
}
.skin-card-meta { display: flex; align-items: center; gap: 8px; font-size: var(--fs-xs); }
.badge.steve { background: var(--steve-bg); color: var(--steve-ink); border-color: transparent; }
.badge.alex { background: var(--alex-bg); color: var(--alex-ink); border-color: transparent; }
.uploader { color: var(--muted); }
.skin-card-actions { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; }
.like-btn {
  border: 1px solid var(--border); background: transparent; color: var(--muted);
  border-radius: var(--r-s, 4px); padding: 4px 8px; min-height: 32px; cursor: pointer; font-size: var(--fs-xs, 12px); font-weight: 500;
}
.like-btn:hover { color: var(--danger); border-color: var(--danger); background: transparent; box-shadow: none; transform: none; }
.like-btn.liked { color: var(--danger); border-color: var(--danger); }
.like-state { margin-left: 4px; font-size: 0.85em; }
.skel-line { height: 16px; border-radius: var(--r-s); }
.skel-line.short { width: 60%; }
</style>
